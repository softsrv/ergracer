package concept2

// Concept2Payload is the top-level webhook body from Concept2.
//
// Confirmed against a real production delivery (2026-07-28) — Concept2's
// own docs describe a {"data": {...}} wrapper, but that does not match
// reality. The actual payload is FLAT, no "data" wrapper:
//
//	result-added / result-updated:
//	  {"type": "result-added", "result": {"id": 118931190, "user_id": 1890109, ...}}
//	result-deleted (structure unconfirmed against a real delivery — no
//	example seen yet; kept flat/defensive rather than nested, consistent
//	with the confirmed shape above):
//	  {"type": "result-deleted", "result_id": ...}
//
// Concept2 does not sign or otherwise authenticate its webhook deliveries,
// and does not send (or document) any subscription-verification/
// challenge-echo mechanism.
type Concept2Payload struct {
	// Type is the event type: "result-added", "result-updated", or
	// "result-deleted".
	Type string `json:"type"`

	// Result is populated for result-added/result-updated events. Only its
	// ID and UserID fields are relied on — RowingService.ProcessResult always
	// re-fetches the authoritative result via the Concept2 API rather than
	// trusting whatever subset of fields the webhook happens to embed here.
	// The real payload includes several fields not modeled on Result at all
	// (timezone, date_utc, source, privacy, stroke_data, real_time, as of the
	// 2026-07-28 sample) — encoding/json silently ignores unknown fields, so
	// this is harmless and nothing needs to change to accommodate them.
	Result *Result `json:"result,omitempty"`

	// ResultID is populated for result-deleted events (a bare ID, no nested
	// result object — unconfirmed against a real delivery, kept defensively).
	ResultID int64 `json:"result_id,omitempty"`
}
