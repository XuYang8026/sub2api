package service

// <fork:oauth-session>
//
// ForkOAuthSessionMirrorRegistration is a wire marker type. The repository
// ForkExtSet provider that enables the Redis OAuth session mirrors returns it,
// and ProvideProxyCircuitBreakerAndRegister (wire_forkext.go) consumes it so
// wire keeps the provider in the graph without touching service/wire.go or
// cmd/server/wire.go.
type ForkOAuthSessionMirrorRegistration struct{}

// </fork>
