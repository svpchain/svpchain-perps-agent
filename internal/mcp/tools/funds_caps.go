package tools

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	sdkmath "cosmossdk.io/math"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/svpchain/svpchain-perps-agent/internal/mcp/limits"
)

// These two tools let an agent read and set the per-symbol daily transfer-out
// cap for ITS OWN authenticated wallet (svp / usdc / usdv). Caps are keyed by
// the owner wallet address, so all of a wallet's concurrent agents and re-auths
// share one cap and one daily total (a fresh login can't reset the meter).
// Caps are fully agent-controlled with no operator config: every symbol starts
// unlimited until the owner sets one. The cap bounds an honest agent's blast
// radius but is not a hard guardrail against a compromised one (which could
// raise its own cap before draining). Caps and usage are in-memory and reset on
// restart / UTC midnight.

// -- set_transfer_out_cap ----------------------------------------------

type SetTransferOutCapInput struct {
	Symbol string `json:"symbol" jsonschema:"token symbol to cap: \"svp\", \"usdc\", or \"usdv\""`
	Amount string `json:"amount" jsonschema:"daily transfer-out cap in human token units, e.g. \"500\" or \"1.5\"; \"0\" means unlimited"`
}

type SetTransferOutCapOutput struct {
	Symbol string `json:"symbol"`
	Cap    string `json:"cap"` // human amount, or "unlimited"
}

func (h *Handlers) SetTransferOutCap(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in SetTransferOutCapInput,
) (*mcp.CallToolResult, SetTransferOutCapOutput, error) {
	tp, err := h.authorize(ctx, "set_transfer_out_cap")
	if err != nil {
		return nil, SetTransferOutCapOutput{}, err
	}
	a, ok := assetForSymbol(in.Symbol)
	if !ok {
		return nil, SetTransferOutCapOutput{}, userErrf(
			"unknown token symbol %q (known: svp, usdc, usdv)", in.Symbol)
	}

	// "0" means unlimited — the zero-disables convention used throughout the
	// limits config.
	if strings.TrimSpace(in.Amount) == "0" {
		h.Deps.TransferOut.SetUnlimited(tp.Owner, a.symbol)
		return nil, SetTransferOutCapOutput{Symbol: a.symbol, Cap: "unlimited"}, nil
	}
	base, err := humanToBaseUnits(in.Amount, a.decimals)
	if err != nil {
		return nil, SetTransferOutCapOutput{}, fmt.Errorf("amount: %w", err)
	}
	h.Deps.TransferOut.SetCap(tp.Owner, limits.SymbolCap{
		Symbol:   a.symbol,
		Decimals: a.decimals,
		CapBase:  base.BigInt(),
	})
	return nil, SetTransferOutCapOutput{Symbol: a.symbol, Cap: humanAmount(base, a.decimals)}, nil
}

// -- get_transfer_out_cap ----------------------------------------------

type GetTransferOutCapInput struct {
	Symbol string `json:"symbol,omitempty" jsonschema:"optional single symbol (svp/usdc/usdv); omit to list all"`
}

type TransferOutCapDTO struct {
	Symbol    string `json:"symbol"`
	Cap       string `json:"cap"`        // human amount, or "unlimited"
	UsedToday string `json:"used_today"` // human amount moved out so far this UTC day
	Remaining string `json:"remaining"`  // human amount, or "unlimited"
}

type GetTransferOutCapOutput struct {
	Caps []TransferOutCapDTO `json:"caps"`
}

func (h *Handlers) GetTransferOutCap(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in GetTransferOutCapInput,
) (*mcp.CallToolResult, GetTransferOutCapOutput, error) {
	tp, err := h.authorize(ctx, "get_transfer_out_cap")
	if err != nil {
		return nil, GetTransferOutCapOutput{}, err
	}
	assets := transferOutAssets
	if s := strings.TrimSpace(in.Symbol); s != "" {
		a, ok := assetForSymbol(s)
		if !ok {
			return nil, GetTransferOutCapOutput{}, userErrf(
				"unknown token symbol %q (known: svp, usdc, usdv)", in.Symbol)
		}
		assets = []assetSymbol{a}
	}
	return nil, GetTransferOutCapOutput{
		Caps: transferOutCapView(h.Deps.TransferOut, tp.Owner, assets),
	}, nil
}

// transferOutCapView builds the per-symbol cap report for an owner: effective
// cap (or "unlimited"), amount already moved out today, and remaining headroom.
// The store methods are nil-safe, so this is testable with a real store and
// without a live handler.
func transferOutCapView(
	store *limits.MemoryTransferOutStore, owner string, assets []assetSymbol,
) []TransferOutCapDTO {
	out := make([]TransferOutCapDTO, 0, len(assets))
	for _, a := range assets {
		hum := func(x *big.Int) string { return humanAmount(sdkmath.NewIntFromBigInt(x), a.decimals) }
		used := store.Used(owner, a.symbol)
		dto := TransferOutCapDTO{
			Symbol:    a.symbol,
			UsedToday: hum(used),
			Cap:       "unlimited",
			Remaining: "unlimited",
		}
		if c, ok := store.Cap(owner, a.symbol); ok && c.CapBase != nil {
			rem := new(big.Int).Sub(c.CapBase, used)
			if rem.Sign() < 0 {
				rem = big.NewInt(0)
			}
			dto.Cap = hum(c.CapBase)
			dto.Remaining = hum(rem)
		}
		out = append(out, dto)
	}
	return out
}
