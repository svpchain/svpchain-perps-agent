package policy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// AuditEntry is one row of the append-only audit log. The MCP server
// writes one entry per signed-tx broadcast (success or chain reject), plus
// one per policy denial so reviewers can reconstruct the full decision
// trail.
type AuditEntry struct {
	Time        time.Time `json:"time"`
	TenantID    string    `json:"tenant_id"`
	Owner       string    `json:"owner"`
	Tool        string    `json:"tool"`
	ClientID    string    `json:"client_id,omitempty"`
	TxHash      string    `json:"tx_hash,omitempty"`
	MsgType     string    `json:"msg_type,omitempty"`
	Subaccount  uint32    `json:"subaccount,omitempty"`
	NotionalUSD string    `json:"notional_usd,omitempty"`
	Code        uint32    `json:"code,omitempty"` // chain CheckTx code; 0 == accepted
	Outcome     string    `json:"outcome"`        // "broadcast" | "policy_denied" | "chain_reject"
	Reason      string    `json:"reason,omitempty"`
}

// Auditor serializes AuditEntry rows as one JSON object per line to an
// io.Writer. v0.1 default is os.Stdout; v0.2 will swap in a rotating file.
type Auditor struct {
	w  io.Writer
	mu sync.Mutex
}

// NewAuditor returns an Auditor writing to w. Pass NewStdoutAuditor() for
// the v0.1 default behavior.
func NewAuditor(w io.Writer) *Auditor { return &Auditor{w: w} }

// NewStdoutAuditor returns an Auditor wired to os.Stdout (v0.1 default).
func NewStdoutAuditor() *Auditor { return NewAuditor(os.Stdout) }

// Append writes one entry. The caller does not need to set Time —
// Append fills it in if zero.
func (a *Auditor) Append(e AuditEntry) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	bz, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err = a.w.Write(append(bz, '\n'))
	return err
}
