package tools

import (
	"context"
	"fmt"
	"strings"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	assettypes "github.com/dydxprotocol/v4-chain/protocol/x/assets/types"
	satypes "github.com/dydxprotocol/v4-chain/protocol/x/subaccounts/types"
	"github.com/svpchain/svpchain-perps-agent/internal/mcp/indexer"
)

// -- get_subaccount (indexer) -------------------------------------------

type GetSubaccountInput struct {
	Address          string `json:"address" jsonschema:"svp1... bech32 owner address"`
	SubaccountNumber uint32 `json:"subaccount_number"`
}

type GetSubaccountOutput struct {
	Subaccount indexer.Subaccount `json:"subaccount"`
}

// GetSubaccount fetches a committed subaccount snapshot from the indexer. Like
// every tenant-scoped tool it authorizes first; upstream's MCP server also had
// auth middleware short-circuiting an unauthenticated call into a soft
// auth_required result before the handler ran. Here internal/a2aserver resolves
// the tenant instead, and an unauthenticated call reaches authorizeOwner and
// fails with ErrNoTenant.
func (h *Handlers) GetSubaccount(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in GetSubaccountInput,
) (*mcp.CallToolResult, GetSubaccountOutput, error) {
	tp, err := h.authorizeOwner(ctx, "get_subaccount", in.Address)
	if err != nil {
		return nil, GetSubaccountOutput{}, err
	}
	if err := h.Deps.Policy.CheckSubaccount(tp.TenantID, in.SubaccountNumber); err != nil {
		return nil, GetSubaccountOutput{}, err
	}
	sa, err := h.Deps.Indexer.GetSubaccount(ctx, in.Address, in.SubaccountNumber)
	if err != nil {
		return nil, GetSubaccountOutput{}, err
	}
	return nil, GetSubaccountOutput{Subaccount: *sa}, nil
}

// -- get_live_subaccount (chain) ----------------------------------------

type GetLiveSubaccountInput struct {
	Owner            string `json:"owner" jsonschema:"svp1... bech32 owner address"`
	SubaccountNumber uint32 `json:"subaccount_number"`
}

// LiveSubaccountDTO is the JSON-friendly projection of satypes.Subaccount.
// The raw proto carries dtypes.SerializableInt for Quantums / FundingIndex
// / QuoteBalance — those fields marshal to JSON strings but the MCP-SDK
// schema reflector sees them as Go structs and demands JSON objects,
// which fails output-validation at runtime against any populated
// subaccount. Mapping to string fields here lets the auto-generated
// schema match the actual JSON shape.
type LiveSubaccountDTO struct {
	Id                 LiveSubaccountIDDTO        `json:"id"`
	AssetPositions     []LiveAssetPositionDTO     `json:"asset_positions"`
	PerpetualPositions []LivePerpetualPositionDTO `json:"perpetual_positions"`
	MarginEnabled      bool                       `json:"margin_enabled"`
}

type LiveSubaccountIDDTO struct {
	Owner  string `json:"owner"`
	Number uint32 `json:"number"`
}

type LiveAssetPositionDTO struct {
	AssetId  uint32 `json:"asset_id"`
	Quantums string `json:"quantums"` // dec string from SerializableInt.String
	Index    uint64 `json:"index"`
}

type LivePerpetualPositionDTO struct {
	PerpetualId  uint32 `json:"perpetual_id"`
	Quantums     string `json:"quantums"`
	FundingIndex string `json:"funding_index"`
	QuoteBalance string `json:"quote_balance"`
}

type GetLiveSubaccountOutput struct {
	Subaccount LiveSubaccountDTO `json:"subaccount"`
}

// liveSubaccountFromChain projects the raw proto into the JSON-friendly
// DTO. Arrays are initialised non-nil so clients that don't tolerate
// `null` get `[]` instead.
func liveSubaccountFromChain(sub satypes.Subaccount) LiveSubaccountDTO {
	out := LiveSubaccountDTO{
		AssetPositions:     []LiveAssetPositionDTO{},
		PerpetualPositions: []LivePerpetualPositionDTO{},
		MarginEnabled:      sub.MarginEnabled,
	}
	if sub.Id != nil {
		out.Id = LiveSubaccountIDDTO{Owner: sub.Id.Owner, Number: sub.Id.Number}
	}
	for _, ap := range sub.AssetPositions {
		if ap == nil {
			continue
		}
		out.AssetPositions = append(out.AssetPositions, LiveAssetPositionDTO{
			AssetId:  ap.AssetId,
			Quantums: ap.Quantums.String(),
			Index:    ap.Index,
		})
	}
	for _, pp := range sub.PerpetualPositions {
		if pp == nil {
			continue
		}
		out.PerpetualPositions = append(out.PerpetualPositions, LivePerpetualPositionDTO{
			PerpetualId:  pp.PerpetualId,
			Quantums:     pp.Quantums.String(),
			FundingIndex: pp.FundingIndex.String(),
			QuoteBalance: pp.QuoteBalance.String(),
		})
	}
	return out
}

func (h *Handlers) GetLiveSubaccount(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in GetLiveSubaccountInput,
) (*mcp.CallToolResult, GetLiveSubaccountOutput, error) {
	tp, err := h.authorizeOwner(ctx, "get_live_subaccount", in.Owner)
	if err != nil {
		return nil, GetLiveSubaccountOutput{}, err
	}
	if err := h.Deps.Policy.CheckSubaccount(tp.TenantID, in.SubaccountNumber); err != nil {
		return nil, GetLiveSubaccountOutput{}, err
	}
	sub, err := h.Deps.Chain.SubaccountQuery.Subaccount(ctx, in.Owner, in.SubaccountNumber)
	if err != nil {
		return nil, GetLiveSubaccountOutput{}, err
	}
	return nil, GetLiveSubaccountOutput{Subaccount: liveSubaccountFromChain(sub)}, nil
}

// -- get_balance (chain x/bank) -----------------------------------------

type GetBalanceInput struct {
	Owner string `json:"owner" jsonschema:"svp1... bech32 owner address whose wallet (bank) balance to read"`
}

// BalanceDTO is one denom's wallet balance. Amount is the authoritative raw
// on-chain integer in the denom's base units; Symbol/Display are a best-effort
// human projection for denoms whose decimals we know (USDC, native SVP) and are
// omitted for any other denom.
//
// Every balance this agent returns is an x/bank denom, so Source is always
// omitted. Upstream also contract-read pure ERC-20 tokens (e.g. USDV, which
// have no x/bank representation) and tagged those Source="erc20" with Denom set
// to the 0x contract address; that path needed an EVM client this binary cannot
// configure and was dropped in the absorption. The field stays so the JSON
// shape is unchanged — see internal/mcp/doc.go.
type BalanceDTO struct {
	Denom   string `json:"denom"`
	Amount  string `json:"amount"`            // base-unit integer (e.g. quantums)
	Symbol  string `json:"symbol,omitempty"`  // human ticker for known denoms
	Display string `json:"display,omitempty"` // decimal-adjusted amount for known denoms
	Source  string `json:"source,omitempty"`  // always empty here; "erc20" upstream
}

type GetBalanceOutput struct {
	Owner    string       `json:"owner"`
	Balances []BalanceDTO `json:"balances"`
}

// knownDenoms maps base denoms to a human ticker + decimal places so
// get_balance can present a decimal-adjusted amount next to the raw integer.
// Unknown denoms are still returned, just without Symbol/Display.
var knownDenoms = map[string]struct {
	Symbol   string
	Decimals int64
}{
	assettypes.UusdcDenom: {Symbol: "USDC", Decimals: 6}, // erc20/usdc
	"asvp":                {Symbol: "SVP", Decimals: 18}, // native gas token (atto-SVP)
}

// GetBalance reads an owner's x/bank wallet balances across all denoms. This is
// the funds the deposit/withdraw tools move into and out of subaccount trading
// collateral — distinct from get_subaccount / get_live_subaccount, which read
// the collateral itself.
func (h *Handlers) GetBalance(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in GetBalanceInput,
) (*mcp.CallToolResult, GetBalanceOutput, error) {
	tp, err := h.authorizeOwner(ctx, "get_balance", in.Owner)
	if err != nil {
		return nil, GetBalanceOutput{}, err
	}
	coins, err := h.Deps.Chain.BankQuery.AllBalances(ctx, in.Owner)
	if err != nil {
		return nil, GetBalanceOutput{}, err
	}
	// Bank balances only. Upstream also merged in contract-read balances for
	// pure ERC-20s (USDV, …) here, but that path was gated on an EVM client
	// this binary never configures, so it could only ever return nil — see
	// internal/mcp/doc.go. BalanceDTO.Source is therefore always "bank".
	return nil, GetBalanceOutput{Owner: tp.Owner, Balances: balancesFromCoins(coins)}, nil
}

// balancesFromCoins projects raw bank coins into JSON-friendly BalanceDTOs,
// attaching Symbol/Display for known denoms. Pure and node-free so it's
// trivially testable. Returns a non-nil slice so empty wallets marshal to
// `[]` rather than `null`.
func balancesFromCoins(coins sdk.Coins) []BalanceDTO {
	balances := make([]BalanceDTO, 0, len(coins))
	for _, c := range coins {
		dto := BalanceDTO{Denom: c.Denom, Amount: c.Amount.String()}
		if meta, ok := knownDenoms[c.Denom]; ok {
			dto.Symbol = meta.Symbol
			dto.Display = humanAmount(c.Amount, meta.Decimals)
		}
		balances = append(balances, dto)
	}
	return balances
}

// humanAmount renders a base-unit integer as a decimal-adjusted string with
// trailing zeros trimmed (LegacyDec.String always pads to 18 places, which
// reads badly): 100_000_000 USDC at 6 dp -> "100", 100_500_000 -> "100.5".
func humanAmount(amount math.Int, decimals int64) string {
	s := math.LegacyNewDecFromIntWithPrec(amount, decimals).String()
	if strings.ContainsRune(s, '.') {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

// humanToBaseUnits is the inverse of humanAmount: it scales a human decimal
// string up to a base-unit integer for a denom with the given decimals,
// rejecting amounts finer than the denom can represent (e.g. "0.0000001" USDC
// at 6 dp). Used by build_bank_send to accept human amounts for known denoms.
func humanToBaseUnits(human string, decimals int64) (math.Int, error) {
	dec, err := math.LegacyNewDecFromStr(human)
	if err != nil {
		return math.Int{}, fmt.Errorf("invalid amount %q: %w", human, err)
	}
	if !dec.IsPositive() {
		return math.Int{}, fmt.Errorf("amount must be > 0")
	}
	scaled := dec.MulInt(math.NewIntWithDecimal(1, int(decimals)))
	if !scaled.Equal(scaled.TruncateDec()) {
		return math.Int{}, fmt.Errorf("amount %q has more precision than %d decimals allow", human, decimals)
	}
	return scaled.TruncateInt(), nil
}

// -- whoami -------------------------------------------------------------

type WhoamiInput struct{}
type WhoamiOutput struct {
	ChainID            string   `json:"chain_id"`
	TenantID           string   `json:"tenant_id"`
	Owner              string   `json:"owner"`
	AllowedSubaccounts []uint32 `json:"allowed_subaccounts"`
	BroadcastMode      string   `json:"broadcast_mode"`
	KillSwitch         bool     `json:"kill_switch"`
}

func (h *Handlers) Whoami(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ WhoamiInput,
) (*mcp.CallToolResult, WhoamiOutput, error) {
	// Deliberate exception to the auth helpers: whoami is a tenant
	// self-introspection escape hatch that must work even when the tenant
	// is kill-switched (so the operator can see the kill_switch flag set).
	// Hence no CheckTenant and no RateLimit.Allow here.
	tc, ok := TenantFrom(ctx)
	if !ok {
		return nil, WhoamiOutput{}, ErrNoTenant
	}
	tp, err := h.Deps.Policy.Tenant(tc.TenantID)
	if err != nil {
		return nil, WhoamiOutput{}, err
	}
	subs := make([]uint32, 0, len(tp.AllowedSubaccounts))
	for s := range tp.AllowedSubaccounts {
		subs = append(subs, s)
	}
	return nil, WhoamiOutput{
		ChainID:            h.ChainID,
		TenantID:           tp.TenantID,
		Owner:              tp.Owner,
		AllowedSubaccounts: subs,
		BroadcastMode:      h.Deps.BroadcastMode,
		KillSwitch:         tp.KillSwitch,
	}, nil
}
