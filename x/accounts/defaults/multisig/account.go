package multisig

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	"cosmossdk.io/core/header"
	"cosmossdk.io/x/accounts/accountstd"
	v1 "cosmossdk.io/x/accounts/defaults/multisig/v1"
	aa_interface_v1 "cosmossdk.io/x/accounts/interfaces/account_abstraction/v1"
	"cosmossdk.io/x/tx/signing"
	"github.com/cosmos/cosmos-sdk/codec"
)

var (
	MembersPrefix   = collections.NewPrefix(0)
	SequencePrefix  = collections.NewPrefix(1)
	ConfigPrefix    = collections.NewPrefix(2)
	ProposalsPrefix = collections.NewPrefix(3)
	VotesPrefix     = collections.NewPrefix(4)
)

const (
	DefaultSigningAlgo = "default"
)

// Compile-time type assertions
var (
	_ accountstd.Interface = (*Account)(nil)
)

type Account struct {
	Members  collections.Map[[]byte, uint64]
	Sequence collections.Sequence
	Config   collections.Item[v1.Config]

	addrCodec address.Codec
	hs        header.Service

	_           *signing.HandlerMap
	customAlgos map[string]SignatureHandler

	Proposals collections.Map[uint64, v1.Proposal]
	Votes     collections.Map[collections.Pair[uint64, []byte], bool]
}

type Options struct {
	CustomAlgorithms map[string]SignatureHandler
}

func NewAccount(name string, handlerMap *signing.HandlerMap, opts Options) accountstd.AccountCreatorFunc {
	return func(deps accountstd.Dependencies) (string, accountstd.Interface, error) {
		return name, &Account{
			Members:   collections.NewMap(deps.SchemaBuilder, MembersPrefix, "participants", collections.BytesKey, collections.Uint64Value),
			Sequence:  collections.NewSequence(deps.SchemaBuilder, SequencePrefix, "sequence"),
			Config:    collections.NewItem(deps.SchemaBuilder, ConfigPrefix, "config", codec.CollValue[v1.Config](deps.LegacyStateCodec)),
			Proposals: collections.NewMap(deps.SchemaBuilder, ProposalsPrefix, "proposals", collections.Uint64Key, codec.CollValue[v1.Proposal](deps.LegacyStateCodec)),
			Votes:     collections.NewMap(deps.SchemaBuilder, VotesPrefix, "votes", collections.PairKeyCodec(collections.Uint64Key, collections.BytesKey), collections.BoolValue),
			addrCodec: deps.AddressCodec,
			// signingHandlers: handlerMap,
			hs: deps.Environment.HeaderService,
		}, nil
	}
}

func (a *Account) Init(ctx context.Context, msg *v1.MsgInit) (*v1.MsgInitResponse, error) {
	if len(msg.PubKeys) != len(msg.Weights) {
		return nil, errors.New("the number of public keys and weights must be equal")
	}

	isValidAlgo := msg.Config.Algo == DefaultSigningAlgo
	allSupportedAlgos := []string{DefaultSigningAlgo} // just to make the error more informative

	// if the algo is not the default, check if it is a custom algo that is supported
	if !isValidAlgo {
		for i := range a.customAlgos {
			if msg.Config.Algo == i {
				isValidAlgo = true
				break
			}
			allSupportedAlgos = append(allSupportedAlgos, i)
		}
	}

	if !isValidAlgo {
		return nil, fmt.Errorf("unsupported signing algo: %s, must be one of: %v", msg.Config.Algo, allSupportedAlgos)
	}

	// set participants
	for i := range msg.PubKeys {
		if err := a.Members.Set(ctx, msg.PubKeys[i], msg.Weights[i]); err != nil {
			return nil, err
		}
	}

	totalWeight := uint64(0)
	for _, weight := range msg.Weights {
		totalWeight += weight
	}

	if err := validateConfig(*msg.Config, totalWeight); err != nil {
		return nil, err
	}

	return &v1.MsgInitResponse{}, nil
}

func (a Account) Vote(ctx context.Context, msg *v1.MsgVote) (*v1.MsgVoteResponse, error) {
	cfg, err := a.Config.Get(ctx)
	if err != nil {
		return nil, err
	}

	var voterBz []byte
	if cfg.Algo == DefaultSigningAlgo {
		// if we are using the default algo, we use the signer as the voter
		voterBz, err = a.addrCodec.StringToBytes(msg.Signer)
		if err != nil {
			return nil, err
		}
	} else {
		voterBz, err = a.customAlgos[cfg.Algo].RecoverPubKey(msg.GetSignature())
		if err != nil {
			return nil, err
		}
	}

	// check if the voter is a member
	_, err = a.Members.Get(ctx, voterBz)
	if err != nil {
		return nil, err
	}

	// check if the proposal exists
	_, err = a.Proposals.Get(ctx, msg.ProposalId)
	if err != nil {
		return nil, err
	}

	// check if the voter has already voted
	_, err = a.Votes.Get(ctx, collections.Join(msg.ProposalId, voterBz))
	if err == nil && !cfg.Revote {
		return nil, errors.New("voter has already voted, can't change its vote per config")
	}
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}

	return &v1.MsgVoteResponse{}, a.Votes.Set(ctx, collections.Join(msg.ProposalId, voterBz), msg.Vote)
}

// Authenticate implements the authentication flow of an abstracted base account.
func (a Account) Authenticate(ctx context.Context, msg *aa_interface_v1.MsgAuthenticate) (*aa_interface_v1.MsgAuthenticateResponse, error) {
	return &aa_interface_v1.MsgAuthenticateResponse{}, nil
}

func validateConfig(cfg v1.Config, totalWeight uint64) error {
	// check for zero values
	if cfg.Threshold == 0 || cfg.Quorum == 0 || cfg.VotingPeriod == 0 {
		return errors.New("threshold, quorum and voting period must be greater than zero")
	}

	// threshold must be less than or equal to the total weight
	if totalWeight < uint64(cfg.Threshold) {
		return errors.New("threshold must be less than or equal to the total weight")
	}

	// quota must be less than or equal to the total weight
	if totalWeight < uint64(cfg.Quorum) {
		return errors.New("quorum must be less than or equal to the total weight")
	}

	return nil
}

func (a Account) QuerySequence(ctx context.Context, _ *v1.QuerySequence) (*v1.QuerySequenceResponse, error) {
	seq, err := a.Sequence.Peek(ctx)
	if err != nil {
		return nil, err
	}
	return &v1.QuerySequenceResponse{Sequence: seq}, nil
}

// RegisterExecuteHandlers implements implementation.Account.
func (a *Account) RegisterExecuteHandlers(builder *accountstd.ExecuteBuilder) {
	accountstd.RegisterExecuteHandler(builder, a.Authenticate) // account abstraction
	accountstd.RegisterExecuteHandler(builder, a.UpdateConfig)
	accountstd.RegisterExecuteHandler(builder, a.Vote)
}

// RegisterInitHandler implements implementation.Account.
func (a *Account) RegisterInitHandler(builder *accountstd.InitBuilder) {
	accountstd.RegisterInitHandler(builder, a.Init)
}

// RegisterQueryHandlers implements implementation.Account.
func (a *Account) RegisterQueryHandlers(builder *accountstd.QueryBuilder) {
	accountstd.RegisterQueryHandler(builder, a.QuerySequence)
}