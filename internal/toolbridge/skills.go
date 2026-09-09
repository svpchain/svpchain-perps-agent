package toolbridge

// Skill IDs. One per operation family on the Agent Card; the registry tags
// every operation with its skill so the card and the dispatch table cannot
// drift (a test pins the mapping).
const (
	SkillMarketData = "svpchain-market-data"
	SkillAccount    = "svpchain-account"
	SkillTrading    = "svpchain-trading"
	SkillFunds      = "svpchain-funds"
	SkillBroadcast  = "svpchain-broadcast"
	SkillAuth       = "svpchain-auth"

	// SkillMeta is this agent describing itself — see meta.go. It is not an
	// operation family; it needs no credential and no backing service.
	SkillMeta = "svpchain-meta"
)

// NewEmpty returns a registry with nothing registered.
func NewEmpty() *Registry { return newRegistry() }
