package lexer_test

// conformanceXFail is the inventory of where YL stands against the YAML Test Suite: every case whose behavior
// diverges from the suite's expectation, mapped to the reason.
//
// Listed entries are reported as expected failures rather than hard ones, so the suite stays green and guards
// against NEW regressions. An entry that starts passing is reported as an error ("remove from xfail"), so the list
// cannot rot into an excuse.
//
// Baseline: 226/272 documents with a JSON equivalent are accepted AND lex to that JSON; 85/94 invalid documents are
// rejected. The 32 entries below group into four causes; 14 of them (multiple documents) are a design boundary
// rather than work.
//
// See CONFORMANCE.md for the full analysis.
func conformanceXFail() map[string]string {
	return map[string]string{
		// ---------------------------------------------------------------------------------------------------
		// 1. Multiple documents (14). A JSON token stream has one root, so YL rejects a multi-document stream.
		// The suite's json field for these holds several JSON values in sequence. Out of scope until the
		// ND-JSON work lands (see the lexers README roadmap), at which point they become an NDJSON mode.
		// ---------------------------------------------------------------------------------------------------
		"27NA":   "multiple documents",
		"2LFX":   "multiple documents",
		"6CK3":   "multiple documents",
		"6LVF":   "multiple documents",
		"BEC7":   "multiple documents",
		"C4HZ":   "multiple documents",
		"CC74":   "multiple documents",
		"DK95/7": "multiple documents",
		"MUS6/2": "multiple documents",
		"P76L":   "multiple documents",
		"RTP8":   "multiple documents",
		"U3C3":   "multiple documents",
		"Z9M4":   "multiple documents",
		"ZYU8":   "multiple documents",

		// ---------------------------------------------------------------------------------------------------
		// 2. Invalid documents we ACCEPT (9). Real conformance bugs, and the most valuable group: we are more
		// permissive than YAML allows, mostly around flow collections, comments and tabs. Inherited from the
		// underlying goccy parser rather than introduced by our walk, so each needs to be confirmed against
		// goccy upstream before we decide whether to pre-validate ourselves.
		// ---------------------------------------------------------------------------------------------------
		"9C9N":   "we accept an invalid document",
		"9JBA":   "we accept an invalid document",
		"CVW2":   "we accept an invalid document",
		"G5U8":   "we accept an invalid document",
		"QB6E":   "we accept an invalid document",
		"SU5Z":   "we accept an invalid document",
		"U99R":   "we accept an invalid document",
		"Y79Y/3": "we accept an invalid document",
		"YJV2":   "we accept an invalid document",

		// ---------------------------------------------------------------------------------------------------
		// 3. Accepted, but the token stream differs from the expected JSON (6). Semantic divergences in how a
		// scalar is resolved -- tags (!!binary, !!str), trailing whitespace, flow nodes.
		//
		// RR7F is not one of them: it is a defect in the FIXTURE. Its yaml is "a: 4.2 / ? d / : 23", and its own
		// event stream and canonical dump both order the keys a, d -- but its json field writes d first. The
		// suite compares loaded data as unordered maps, so nothing there ever checked its json text against its
		// own tree. We keep key order (a JSON token stream is ordered, and so is our model), so we match the
		// event stream and diverge from the json text. Reported upstream rather than worked around.
		// ---------------------------------------------------------------------------------------------------
		"RR7F":   "fixture defect: its json field contradicts its own event stream on key order",
		"565N":   "token stream differs from the expected JSON",
		"L24T/1": "token stream differs from the expected JSON",
		"LE5A":   "token stream differs from the expected JSON",
		"S4JQ":   "token stream differs from the expected JSON",
		"UGM3":   "token stream differs from the expected JSON",

		// ---------------------------------------------------------------------------------------------------
		// 4. Valid documents rejected by the underlying parser (3). goccy fails to parse these at all, so the
		// fix is upstream (or a pre-pass of our own).
		// ---------------------------------------------------------------------------------------------------
		"4MUZ/2": "goccy fails to parse a valid document",
		"DK95/4": "goccy fails to parse a valid document",
		"VJP3/1": "goccy fails to parse a valid document",
	}
}
