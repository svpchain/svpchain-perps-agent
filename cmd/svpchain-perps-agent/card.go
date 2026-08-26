package main

import "github.com/svpchain/svpchain-perps-agent/internal/a2aserver"

// identity is this agent's public face: the name, version, and description its
// Agent Card advertises.
//
// It lives here rather than under internal/a2aserver to keep product identity
// separate from card machinery: the skill text there describes what the agent
// can do, this describes who it is.
//
// ★ These bytes are load-bearing. The served card is hashed and published on
// chain by `deploy.sh --register`, and a verifier fetches the card and
// recomputes that hash. Editing anything here changes the card, so the
// deployment must re-run --register afterwards or the agent reads as
// unverified. The golden test beside this file is what makes such a change
// deliberate.
var identity = a2aserver.CardIdentity{
	Name:    "svpchain-perps-agent",
	Version: "0.1.0",
	Description: "Perpetuals-trading agent for the SVP-Chain DEX: market data, accounts, " +
		"unsigned order and funds tx building, broadcast, and self-service auth.",
}
