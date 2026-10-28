package control

// Normalize fixed defaults before hashing. Environment-dependent defaults are
// resolved once when building the durable execution at admission.
func normalizeArguments(args any) any {
	switch a := args.(type) {
	case StartRequest:
		if a.Context == "" {
			a.Context = "service"
		}
		if a.Mode == "" {
			a.Mode = "finite"
		}
		return a
	case ExecRequest:
		if a.TimeoutSeconds == 0 {
			a.TimeoutSeconds = 10
		}
		return a
	case PowerRequest:
		if a.WaitUntil == "" {
			a.WaitUntil = "ready"
		}
		return a
	case InputNodeRequest:
		if oneOf(a.Action, "click", "drag") && a.Button == 0 {
			a.Button = 1
		}
		return a
	}
	return args
}
