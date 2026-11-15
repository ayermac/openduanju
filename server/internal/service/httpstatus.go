package service

import "context"

// StatusHolder carries the HTTP status a handler selected for the response.
// The HTTP transport injects it into the request context before the handler
// runs; a handler that finishes its work asynchronously sets the code here so
// the transport answers with the status the API contract requires.
type StatusHolder struct {
	// Code is the status the response must use. Zero keeps the transport
	// default, which is also the behaviour for error responses.
	Code int
	// Cookies are the Set-Cookie header values of the response. The handler
	// cannot write headers through the generated signature, so the transport
	// applies them before the body.
	Cookies []string
}

// statusHolderKey is the private context key of the holder.
type statusHolderKey struct{}

// WithStatusHolder returns a context that carries holder.
func WithStatusHolder(ctx context.Context, holder *StatusHolder) context.Context {
	return context.WithValue(ctx, statusHolderKey{}, holder)
}

// StatusHolderFrom returns the holder of ctx. It returns nil when the
// transport did not provide one, for example on a gRPC call.
func StatusHolderFrom(ctx context.Context) *StatusHolder {
	holder, _ := ctx.Value(statusHolderKey{}).(*StatusHolder)
	return holder
}

// SetAccepted marks the response as accepted. It reports whether the
// transport could record the status; over gRPC the status is implied by the
// response message and the call still succeeds.
func SetAccepted(ctx context.Context) bool {
	return setStatus(ctx, 202)
}

// SetCreated marks the response as created.
func SetCreated(ctx context.Context) bool {
	return setStatus(ctx, 201)
}

// setStatus records the selected status, if the transport provided a holder.
func setStatus(ctx context.Context, code int) bool {
	holder := StatusHolderFrom(ctx)
	if holder == nil {
		return false
	}
	holder.Code = code
	return true
}
