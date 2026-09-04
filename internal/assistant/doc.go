// Package assistant answers a natural-language request by planning and running
// MCP tool calls, then writing the answer.
//
// It is the one part of this agent that is model-driven. Everything else is a
// lookup: a request names a skill, a tool and its arguments, and the executor
// dispatches it. That stays true — this is an additional skill on the card,
// not a change to how the existing ones work, and a caller that knows which
// tool it wants should still call that tool directly and get a typed reply for
// no model cost.
//
// # What contains the blast radius
//
// The obvious worry about putting a model in front of a trading surface is
// that it does something expensive. The custody design already answers most of
// it: this agent holds no key and cannot sign. Every write tool returns an
// UNSIGNED payload that the caller inspects and signs with its own key. So a
// model that picks the wrong tool, or the right tool with wrong arguments,
// produces a payload the caller declines to sign. It cannot move funds.
//
// That is most of the answer but not all of it, because three tools do not fit
// the pattern. So the planner is given a subset of the catalog:
//
//   - broadcast_signed_tx lands a transaction the caller already signed.
//     Signing is the caller's moment of consent, and it consented to submit
//     that transaction, not to have a model decide when. Withheld.
//   - set_transfer_out_cap moves a safety limit, server-side, with no
//     signature anywhere. A model that can raise a cap can weaken the control
//     that exists to bound it. Withheld.
//   - auth_challenge and auth_verify are the handshake. A request only reaches
//     this package with a bearer already resolved, so there is nothing here
//     for the planner to do with them, and minting a second identity mid-plan
//     is a way to get confused rather than a capability. Withheld.
//
// Everything else — every read, and every builder that returns an unsigned
// payload — is available. WithheldTools names the set in one place so the
// boundary is a list, not an argument.
//
// # The caller's identity, not the agent's
//
// Every tool call the planner makes carries the bearer of the A2A caller that
// asked the question, through mcpclient.Identity. The model never sees the
// bearer and cannot influence it: it chooses a tool name and arguments, and
// the credential is attached beneath it. So the planner can only ever read
// what its caller could already read, and a prompt that talks the model into
// requesting someone else's subaccount gets that caller's own authorization
// applied to the attempt, which fails at the server.
//
// # Bounds
//
// A planning loop that will not stop is a bill. Three limits, all
// configurable: iterations of the model loop, total tool calls across the
// whole request, and a wall-clock deadline over everything. Hitting one is not
// an error — the loop stops and the model is asked to answer with what it has,
// so a caller gets a partial answer that says so rather than a timeout.
package assistant
