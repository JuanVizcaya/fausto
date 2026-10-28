// Package contract is the payments module's public interface: the only package
// other modules may import. Methods take and return plain structs defined
// here, never domain types, so every call can later become gRPC.
package contract

// Service is the payments module's contract.
type Service interface{}
