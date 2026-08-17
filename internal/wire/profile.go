package wire

import (
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/tools"

	"github.com/svpchain/svpchain-perps-agent/internal/agentchain"
	"github.com/svpchain/svpchain-perps-agent/internal/delegated"
	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

// Profile selects which operation families a binary registers.
//
// This binary ships exactly one — PerpsProfile — but the indirection stays
// because it is what keeps registration a named, testable composition rather
// than a hardcoded sequence inside BuildProfile. TestPerpsProfileCoversTheWhole
// Surface reads it to prove nothing in toolbridge is unreachable.
//
// It used to carry BuildEVM / BuildLendora / RunMarkets flags, gating dependency
// construction as well as registration, so that an agent handed a full config
// would not dial endpoints for families it never served. With the EVM and
// Lendora surfaces gone there is nothing left to gate: everything this binary
// constructs, it serves.
type Profile struct {
	Name string

	// Register composes the binary's operation registry. exec is nil on
	// keyless deployments; the execution registrations then advertise
	// informative refusals.
	Register func(r *toolbridge.Registry, h *tools.Handlers, agent *agentchain.Service, exec *delegated.Service)
}

// RegisterDelegationStack adds the families every delegation-capable binary
// serves: self-service auth, the agent-chain identity modules, and the
// domain-agnostic execution core (identity, self-registration, settlement).
//
// Kept separate from the perps families it sits beside in PerpsProfile because
// the split is real: this is the domain-agnostic half, and TestExecutionCore
// PerpsSplit pins the partition.
func RegisterDelegationStack(r *toolbridge.Registry, h *tools.Handlers, agent *agentchain.Service, exec *delegated.Service) {
	r.RegisterAuth(h)
	r.RegisterAgentChain(agent)
	r.RegisterExecutionCore(exec)
}

// PerpsProfile serves the perpetuals DEX: market data, accounts, order and
// funds building, the Cosmos broadcast rail, and delegated perps execution.
var PerpsProfile = Profile{
	Name: "perps",
	Register: func(r *toolbridge.Registry, h *tools.Handlers, agent *agentchain.Service, exec *delegated.Service) {
		r.RegisterMarketData(h)
		r.RegisterAccount(h)
		r.RegisterTrading(h)
		r.RegisterFunds(h)
		r.RegisterBroadcast(h)
		r.RegisterExecutionPerps(exec)
		RegisterDelegationStack(r, h, agent, exec)
	},
}
