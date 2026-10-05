package postgres

// ActionRefusal is a guard refusal an owner or agent can act on: a stable
// snake_case code, the human message, and the next step. The HTTP layer
// returns it as 409 {"error": {"code", "message", "hint"}}.
type ActionRefusal struct {
	Code    string
	Message string
	Hint    string
}

func (refusal ActionRefusal) Error() string { return refusal.Message }

func refuse(code, message, hint string) error {
	return ActionRefusal{Code: code, Message: message, Hint: hint}
}
