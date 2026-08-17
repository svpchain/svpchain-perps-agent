package chain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseBroadcastError(t *testing.T) {
	cases := []struct {
		name         string
		in           BroadcastResult
		wantTyped    bool
		wantGot      string
		wantRequired string
	}{
		{
			name:         "happy path with wrapped sentinel",
			in:           BroadcastResult{Code: 13, RawLog: "insufficient fees; got: 100stake required: 1000stake: insufficient fee"},
			wantTyped:    true,
			wantGot:      "100stake",
			wantRequired: "1000stake",
		},
		{
			name:         "singular 'fee' (older sdk phrasing) without trailing sentinel",
			in:           BroadcastResult{Code: 13, RawLog: "insufficient fee; got: 0uatom required: 5000uatom"},
			wantTyped:    true,
			wantGot:      "0uatom",
			wantRequired: "5000uatom",
		},
		{
			name:      "code 0 — short-circuit even if RawLog matches",
			in:        BroadcastResult{Code: 0, RawLog: "insufficient fees; got: 1 required: 2"},
			wantTyped: false,
		},
		{
			name:      "unrelated CheckTx reject — falls through to nil",
			in:        BroadcastResult{Code: 18, RawLog: "out of gas in location"},
			wantTyped: false,
		},
		{
			name:      "malformed insufficient-fee log — falls through, not a panic",
			in:        BroadcastResult{Code: 13, RawLog: "insufficient fees; got:"},
			wantTyped: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ParseBroadcastError(tc.in)
			if !tc.wantTyped {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var ife *ErrInsufficientFee
			require.True(t, errors.As(err, &ife), "want *ErrInsufficientFee, got %T", err)
			require.Equal(t, tc.wantGot, ife.Got)
			require.Equal(t, tc.wantRequired, ife.Required)
		})
	}
}

func TestErrInsufficientFee_ErrorString(t *testing.T) {
	e := &ErrInsufficientFee{Got: "100stake", Required: "1000stake"}
	require.Equal(t, "insufficient fee: got 100stake, required 1000stake", e.Error())
}
