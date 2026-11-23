// Package events is the SSE Event Stream that publishes operation output and live metrics.
//
// A Hub assigns each published event a global id and fans it out to the
// connected clients without ever blocking the publisher. A client that falls
// behind is dropped, and nothing is replayed after a reconnect. Handler serves
// one stream (docs/api.md section 5). Call Hub.Shutdown before the HTTP
// server's Shutdown so open streams end.
package events
