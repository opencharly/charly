// CUE schema for the `android` kind. #Android validates ONE kind:android entity
// (a device). A device is EITHER an in-pod emulator hosted by a kind:box (box)
// XOR a remote/physical adb endpoint (adb) — never both, never neither. Shared
// #Step from _common.cue.
//
// CLOSED: the base struct (no trailing `...`) models every authored key, so an
// unknown key is still a typo. The box⊻adb exactly-one mutual-exclusion is
// enforced in GO (validateAndroidDevices in unified.go, the load-time sibling of
// validateCheckBeds) — NOT by a trailing `& ({box:_} | {adb:_})` disjunction:
// `cue exp gengotypes` collapses an entity-level top-level disjunction to an
// EMPTY `struct{}`, which would make spec.Android useless as a drop-in for
// AndroidSpec. The plain closed struct below generates the full field set, and
// the Go rule restores the exactly-one-of-box/adb XOR.
#Android: {
	serial?:         string & !=""
	device?:         string & !=""
	api_level?:      int & >=1     @go(ApiLevel,type=int)
	google_account?: #GoogleAccount @go(GoogleAccount,optional=nillable)
	plan?: [...#Step]

	// Device source — exactly one of box/adb (enforced in Go, see header).
	box?: string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	adb?: #AdbEndpoint @go(Adb,optional=nillable)
}

// adb endpoint host is "host:port" but MAY carry a ${HOST_PORT:N} var-ref
// (resolved at deploy time), so only non-emptiness is enforced.
#AdbEndpoint: {
	host: string & !=""
}

#GoogleAccount: {
	email_secret?: string & !="" @go(EmailSecret)
	token_secret?: string & !="" @go(TokenSecret)
}
