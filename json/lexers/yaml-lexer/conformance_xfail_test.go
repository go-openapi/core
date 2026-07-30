package lexer_test

// conformanceXFail is the inventory of where YL stands against the YAML Test Suite: every case whose behavior
// diverges from the suite's expectation, mapped to the reason.
//
// Listed entries are reported as expected failures rather than hard ones, so the suite stays green and guards
// against NEW regressions. An entry that starts passing is reported as an error ("remove from xfail"), so the list
// cannot rot into an excuse.
//
// Baseline: 204/272 documents with a JSON equivalent are accepted AND lex to that JSON; 85/94 invalid documents are
// rejected. The 54 entries below group into five causes, in descending order of how much work they represent.
//
// See CONFORMANCE.md for the full analysis.
func conformanceXFail() map[string]string {
	return map[string]string{
		// ---------------------------------------------------------------------------------------------------
		// 1. Non-scalar mapping keys (23). YL requires an object key to be a scalar, since JSON keys are
		// strings. But most of these are keys that RESOLVE to a string -- an alias (`*a : v`), an anchored or
		// tagged scalar (`&a k : v`, `!!str k : v`) -- and the suite's own JSON expectation shows the resolved
		// string as the key. Supporting them is a real feature gap, not a scope boundary. The genuinely
		// out-of-scope ones are complex keys (`? [a, b] : c`), which JSON cannot express at all.
		// ---------------------------------------------------------------------------------------------------
		"26DV": "non-scalar mapping key",
		"2SXE": "non-scalar mapping key",
		"2XXW": "non-scalar mapping key",
		"5WE3": "non-scalar mapping key",
		"74H7": "non-scalar mapping key",
		"7BMT": "non-scalar mapping key",
		"7FWL": "non-scalar mapping key",
		"7W2P": "non-scalar mapping key",
		"A2M4": "non-scalar mapping key",
		"CN3R": "non-scalar mapping key",
		"CT4Q": "non-scalar mapping key",
		"E76Z": "non-scalar mapping key",
		"GH63": "non-scalar mapping key",
		"HMQ5": "non-scalar mapping key",
		"JTV5": "non-scalar mapping key",
		"L94M": "non-scalar mapping key",
		"RR7F": "non-scalar mapping key",
		"S9E8": "non-scalar mapping key",
		"U3XV": "non-scalar mapping key",
		"WZ62": "non-scalar mapping key",
		"X8DW": "non-scalar mapping key",
		"ZH7C": "non-scalar mapping key",
		"ZWK4": "non-scalar mapping key",

		// ---------------------------------------------------------------------------------------------------
		// 2. Multiple documents (14). A JSON token stream has one root, so YL rejects a multi-document stream.
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
		// 3. Invalid documents we ACCEPT (9). Real conformance bugs, and the most valuable group: we are more
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
		// 4. Accepted, but the token stream differs from the expected JSON (5). Semantic divergences in how a
		// scalar is resolved -- tags (!!binary, !!str), trailing whitespace, flow nodes. Each needs reading
		// individually; they are the subtlest of the five groups because nothing errors.
		// ---------------------------------------------------------------------------------------------------
		"565N":   "token stream differs from the expected JSON",
		"L24T/1": "token stream differs from the expected JSON",
		"LE5A":   "token stream differs from the expected JSON",
		"S4JQ":   "token stream differs from the expected JSON",
		"UGM3":   "token stream differs from the expected JSON",

		// ---------------------------------------------------------------------------------------------------
		// 5. Valid documents rejected by the underlying parser (3). goccy fails to parse these at all, so the
		// fix is upstream (or a pre-pass of our own).
		// ---------------------------------------------------------------------------------------------------
		"4MUZ/2": "goccy fails to parse a valid document",
		"DK95/4": "goccy fails to parse a valid document",
		"VJP3/1": "goccy fails to parse a valid document",
	}
}
