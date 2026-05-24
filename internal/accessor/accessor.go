package accessor

// Token represents the internal permission to access and modify unexported column fields.
// Since it resides in an "internal/" package, external plugins cannot import or construct it.
type Token struct{}
