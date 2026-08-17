package builder_test

import (
	"testing"

	// Blank import sets the sdk bech32 prefix to "svp" via init() —
	// required for any test that runs Msg*.ValidateBasic on a bech32
	// address or SubaccountId. Single point of registration here so each
	// _test.go file in this package doesn't need its own copy.
	_ "github.com/dydxprotocol/v4-chain/protocol/app/config"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/builder"
)

// testOwner is a real bech32 address used as the tenant owner across
// builder tests. The exact value matters: ValidateBasic on SubaccountId
// requires a parsable bech32 with the "svp" prefix; placeholders like
// "svp1tester" fail the prefix check. Borrowed from
// scripts/fullflow_test.sh.
const testOwner = "svp199tqg4wdlnu4qjlxchpd7seg454937hjk505pe"

// Test fee params threaded into newTestAsm so non-CLOB builder tests can
// assert the fee lands on the payload. Mirrors the server config defaults.
const (
	testFeeDenom    = "asvp"
	testFeeAmount   = "25000000000000000"
	testFeeGasLimit = uint64(1_000_000)
)

// newTestAsm constructs an Assembler bound to a stub chain id and the test
// fee params. Builder tests don't broadcast, so the chain id only ends up in
// TxPayload metadata.
func newTestAsm(t *testing.T) *builder.Assembler {
	t.Helper()
	return builder.NewAssembler("test-chain", testFeeDenom, testFeeAmount, testFeeGasLimit)
}
