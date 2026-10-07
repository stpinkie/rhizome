// Package econ implements paired-settlement billing for the mesh: the
// negotiated wire terms (caller→callee), the signed charge receipt
// (callee→caller), the price-sheet math, and — from Track 140 — the
// bilateral ledger both sides keep.
//
// All amounts are canonical non-negative decimal strings (digits plus a
// single '.', no sign/exponent/fraction) so they serialize byte-stably and
// compute exactly in big.Rat — no float, no stringly rounding.
package econ

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/stpinkie/rhizome/pkg/config"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// Terms is the caller-side economy negotiation attached to a signed
// request. It is emitted only when the callee's capability manifest
// advertises `economy` — unnegotiated requests are byte-identical to
// pre-economy encodings.
type Terms struct {
	// Accept marks the caller's consent to be charged under the callee's
	// advertised sheet. Always true when the field is present.
	Accept bool `json:"accept"`
	// MaxCharge caps the total charge in the negotiated unit (canonical
	// decimal; empty = uncapped — the caller accepts the sheet as-is).
	MaxCharge string `json:"max_charge,omitempty"`
	// Unit is the unit the caller agrees to settle in — must be one of the
	// callee's accepted units ({advert.unit} ∪ advert.accepts).
	Unit string `json:"unit"`
}

// Charge is the callee-side signed receipt attached to a response. It is
// emitted only when the request carried Terms, the caller is in the
// callee's bill_peers, and the run succeeded — failed work is never
// billed. Usage carries the metered snapshot regardless of want_usage so
// the receipt is self-contained evidence.
type Charge struct {
	Unit        string                  `json:"unit"`
	Amount      string                  `json:"amount"`
	Usage       *toolshared.RemoteUsage `json:"usage,omitempty"`
	SheetDigest string                  `json:"sheet_digest"`
	Truncated   bool                    `json:"truncated,omitempty"`
}

// Rejection classes (error prefixes the caller can branch on):
//
//	price_floor: — sheet.min_charge exceeds the caller's max_charge; the
//	    callee refuses upfront, before any execution.
//	econ: — a billed caller omitted Terms, or named a unit the callee
//	    does not accept.
const (
	RejectPriceFloor = "price_floor:"
	RejectEcon       = "econ:"
	// MismatchCharge is the caller-side error class when a received
	// Charge fails digest or recompute verification.
	MismatchCharge = "econ:charge_mismatch"
)

// SheetDigest commits a charge to the exact advert it was computed
// against — sha256 over the canonical advert encoding.
func SheetDigest(a *config.EconAdvert) string {
	if a == nil {
		return ""
	}
	raw, err := json.Marshal(a) // struct marshal is deterministic
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// AcceptedUnits is the callee's accepted-unit set: its own sheet unit
// plus every entry in accept_units (the operator warrants sheet numerics
// hold in those units).
func AcceptedUnits(e *config.MeshEconomyConfig) map[string]bool {
	out := map[string]bool{}
	if e == nil {
		return out
	}
	if e.Unit != "" {
		out[e.Unit] = true
	}
	for _, u := range e.AcceptUnits {
		out[u] = true
	}
	return out
}

// IsBillable reports whether the peer is on the callee's billing
// allowlist — trust alone never implies billing.
func IsBillable(e *config.MeshEconomyConfig, peerID string) bool {
	if e == nil || !e.Enabled {
		return false
	}
	for _, p := range e.BillPeers {
		if p == peerID {
			return true
		}
	}
	return false
}

// Negotiate validates the caller's terms against the callee's economy
// config. It returns nil when the caller is not billed (unnegotiated
// peers run as before), a price_floor:/econ:-prefixed rejection
// otherwise.
func Negotiate(e *config.MeshEconomyConfig, peerID string, t *Terms) error {
	if !IsBillable(e, peerID) {
		return nil
	}
	if t == nil || !t.Accept {
		return fmt.Errorf("%s billing peer must negotiate economy terms", RejectEcon)
	}
	if !AcceptedUnits(e)[t.Unit] {
		return fmt.Errorf("%s unit %q not accepted", RejectEcon, t.Unit)
	}
	if priceFloorExceeded(e.PriceSheet.MinCharge, t.MaxCharge) {
		return fmt.Errorf(
			"%s sheet min_charge %s exceeds caller max_charge %s",
			RejectPriceFloor, e.PriceSheet.MinCharge, t.MaxCharge)
	}
	return nil
}

// priceFloorExceeded reports whether the sheet's smallest billable amount
// exceeds the caller's cap — the upfront refusal condition.
func priceFloorExceeded(minCharge, maxCharge string) bool {
	minR, okMin := parseDecimal(minCharge)
	maxR, okMax := parseDecimal(maxCharge)
	if !okMin || !okMax {
		return false
	}
	return minR.Cmp(maxR) > 0
}

// Compute prices a metered run against the sheet:
//
//	sheet·usage = per_task
//	    + per_1k_prompt_tokens     * prompt_tokens     / 1000
//	    + per_1k_completion_tokens * completion_tokens / 1000
//	    + per_second               * duration_ms       / 1000
//	amount    = max(min_charge, min(sheet·usage, max_charge))
//	truncated = sheet·usage exceeded max_charge (buyer pays the cap;
//	            the seller still signals the sheet was higher).
//
// All inputs are canonical decimals, so products stay exact in big.Rat
// (denominators are 2^a·5^b) and render finitely.
func Compute(sheet config.EconPriceSheet, usage *toolshared.RemoteUsage, maxCharge string) (
	amount string, truncated bool, err error,
) {
	total := new(big.Rat)
	add := func(price string, units *big.Rat) {
		p, ok := parseDecimal(price)
		if !ok {
			return
		}
		total.Add(total, new(big.Rat).Mul(p, units))
	}
	add(sheet.PerTask, big.NewRat(1, 1))
	if usage != nil {
		add(sheet.Per1KPromptTokens,
			new(big.Rat).Quo(new(big.Rat).SetInt64(int64(usage.PromptTokens)), big.NewRat(1000, 1)))
		add(sheet.Per1KCompletionTokens,
			new(big.Rat).Quo(new(big.Rat).SetInt64(int64(usage.CompletionTokens)), big.NewRat(1000, 1)))
		add(sheet.PerSecond,
			new(big.Rat).Quo(new(big.Rat).SetInt64(usage.DurationMS), big.NewRat(1000, 1)))
	}
	if maxR, ok := parseDecimal(maxCharge); ok && total.Cmp(maxR) > 0 {
		total = maxR
		truncated = true
	}
	if minR, ok := parseDecimal(sheet.MinCharge); ok && total.Cmp(minR) < 0 {
		total = minR
	}
	return decimalString(total), truncated, nil
}

// VerifyCharge is the caller-side check on a received charge: the sheet
// digest must match the cached advert, the unit must be accepted, and the
// amount must not exceed the charged bound max(min_charge, sheet·usage)
// (floor bumps legitimately exceed raw usage price).
func VerifyCharge(advert *config.EconAdvert, c *Charge) error {
	if advert == nil || c == nil {
		return fmt.Errorf("%s missing advert or charge", MismatchCharge)
	}
	if c.SheetDigest == "" || c.SheetDigest != SheetDigest(advert) {
		return fmt.Errorf("%s sheet_digest does not match advertised terms", MismatchCharge)
	}
	accepted := map[string]bool{advert.Unit: true}
	for _, u := range advert.Accepts {
		accepted[u] = true
	}
	if !accepted[c.Unit] {
		return fmt.Errorf("%s charge unit %q not in advertised accepts", MismatchCharge, c.Unit)
	}
	amountR, ok := parseDecimal(c.Amount)
	if !ok {
		return fmt.Errorf("%s charge amount %q is not a decimal", MismatchCharge, c.Amount)
	}
	bound, _, err := Compute(advert.PriceSheet, c.Usage, "")
	if err != nil {
		return fmt.Errorf("%s recompute failed: %w", MismatchCharge, err)
	}
	boundR, _ := parseDecimal(bound)
	if amountR.Cmp(boundR) > 0 {
		return fmt.Errorf(
			"%s charged %s exceeds sheet bound %s", MismatchCharge, c.Amount, bound)
	}
	return nil
}

// parseDecimal parses a canonical decimal string (empty → not ok).
func parseDecimal(v string) (*big.Rat, bool) {
	if v == "" {
		return nil, false
	}
	for _, c := range v {
		if (c < '0' || c > '9') && c != '.' {
			return nil, false
		}
	}
	r, ok := new(big.Rat).SetString(v)
	if !ok {
		return nil, false
	}
	if r.Sign() < 0 {
		return nil, false
	}
	return r, true
}

// decimalString renders a non-negative Rat as a canonical decimal. Rat
// denominators from sheet arithmetic are 2^a·5^b (finite decimals); a
// defensive 8-place truncation covers any unexpected residue.
func decimalString(r *big.Rat) string {
	num := new(big.Int).Set(r.Num())
	denom := new(big.Int).Set(r.Denom())

	// Count factors of 2 and 5 in the denominator; any other residue means
	// a non-decimal fraction (unreachable with canonical inputs).
	d := new(big.Int).Set(denom)
	two, five := big.NewInt(2), big.NewInt(5)
	q, rem := new(big.Int), new(big.Int)
	var c2, c5 int
	for {
		q.QuoRem(d, two, rem)
		if rem.Sign() != 0 {
			break
		}
		d.Set(q)
		c2++
	}
	for {
		q.QuoRem(d, five, rem)
		if rem.Sign() != 0 {
			break
		}
		d.Set(q)
		c5++
	}
	if d.Cmp(big.NewInt(1)) != 0 {
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(8), nil)
		scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(pow))
		trunc := new(big.Int).Quo(scaled.Num(), scaled.Denom())
		return insertDecimal(trunc.String(), 8)
	}
	scale := c2
	if c5 > c2 {
		scale = c5
	}
	if scale > 0 {
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
		num.Mul(num, pow)
		num.Quo(num, denom)
	}
	return insertDecimal(num.String(), scale)
}

// insertDecimal places a decimal point scale digits from the right of a
// digit string ("1234", 2 → "12.34"; "5", 3 → "0.005"), then trims
// trailing zeros and a dangling point.
func insertDecimal(digits string, scale int) string {
	if scale == 0 {
		return digits
	}
	for len(digits) <= scale {
		digits = "0" + digits
	}
	i := len(digits) - scale
	s := digits[:i] + "." + digits[i:]
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" {
		s = "0"
	}
	return s
}
