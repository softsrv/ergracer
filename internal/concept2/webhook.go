package concept2

// Concept2Payload is the top-level webhook body from Concept2, confirmed
// against https://log.concept2.com/developers/documentation/#header-create-or-update-result:
//
//	result-added / result-updated:
//	  {"data": {"type": "result-added", "result": {"id": 3, "user_id": 1, ...}}}
//	result-deleted:
//	  {"data": {"type": "result-deleted", "result_id": 745}}
//
// Concept2 does not document any subscription-verification/challenge-echo
// mechanism anywhere on that page — an earlier version of this struct had a
// Challenge field for that, which was never real (confirmed absent on two
// separate documentation fetches) and has been removed.
type Concept2Payload struct {
	Data Concept2PayloadData `json:"data"`
}

// Concept2PayloadData is the "data" wrapper object within a webhook delivery.
type Concept2PayloadData struct {
	// Type is the event type: "result-added", "result-updated", or
	// "result-deleted".
	Type string `json:"type"`

	// Result is populated for result-added/result-updated events. Only its
	// ID and UserID fields are relied on — RowingService.ProcessResult always
	// re-fetches the authoritative result via the Concept2 API rather than
	// trusting whatever subset of fields the webhook happens to embed here,
	// so the rest of Result's fields are along for the ride but unused by
	// this path.
	Result *Result `json:"result,omitempty"`

	// ResultID is populated for result-deleted events (a bare ID at the
	// data level, not nested under a result object).
	ResultID int64 `json:"result_id,omitempty"`
}
