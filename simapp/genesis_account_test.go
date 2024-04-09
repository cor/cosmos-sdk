package simapp_test

import (
	"testing"
	"time"

	"github.com/cometbft/cometbft/crypto"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/simapp"
	authtypes "cosmossdk.io/x/auth/types"

	codectestutil "github.com/cosmos/cosmos-sdk/codec/testutil"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func TestSimGenesisAccountValidate(t *testing.T) {
	pubkey := secp256k1.GenPrivKey().PubKey()

	ac := codectestutil.CodecOptions{}.GetAddressCodec()
	addr, err := ac.BytesToString(pubkey.Address())
	require.NoError(t, err)
	modAddr, err := ac.BytesToString(crypto.AddressHash([]byte("testmod")))
	require.NoError(t, err)
	vestingStart := time.Now().UTC()

	coins := sdk.NewCoins(sdk.NewInt64Coin("test", 1000))
	baseAcc := authtypes.NewBaseAccount(addr, pubkey, 0, 0)

	testCases := []struct {
		name    string
		sga     simapp.SimGenesisAccount
		wantErr bool
	}{
		{
			"valid basic account",
			simapp.SimGenesisAccount{
				BaseAccount: baseAcc,
			},
			false,
		},
		{
			"invalid basic account with mismatching address/pubkey",
			simapp.SimGenesisAccount{
				BaseAccount: authtypes.NewBaseAccount(addr, secp256k1.GenPrivKey().PubKey(), 0, 0),
			},
			true,
		},
		{
			"valid basic account with module name",
			simapp.SimGenesisAccount{
				BaseAccount: authtypes.NewBaseAccount(modAddr, nil, 0, 0),
				ModuleName:  "testmod",
			},
			false,
		},
		{
			"valid basic account with invalid module name/pubkey pair",
			simapp.SimGenesisAccount{
				BaseAccount: baseAcc,
				ModuleName:  "testmod",
			},
			true,
		},
		{
			"valid basic account with valid vesting attributes",
			simapp.SimGenesisAccount{
				BaseAccount:     baseAcc,
				OriginalVesting: coins,
				StartTime:       vestingStart.Unix(),
				EndTime:         vestingStart.Add(1 * time.Hour).Unix(),
			},
			false,
		},
		{
			"valid basic account with invalid vesting end time",
			simapp.SimGenesisAccount{
				BaseAccount:     baseAcc,
				OriginalVesting: coins,
				StartTime:       vestingStart.Add(2 * time.Hour).Unix(),
				EndTime:         vestingStart.Add(1 * time.Hour).Unix(),
			},
			true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantErr, tc.sga.Validate(ac) != nil)
		})
	}
}
