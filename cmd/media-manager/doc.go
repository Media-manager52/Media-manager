// Command media-manager runs the Media Manager API, the durable outbox worker,
// and the administrative CLI from a single binary.
//
// Commands: serve (default), migrate, version, and healthcheck. Configuration
// is read from the environment and <NAME>_FILE secret files in run().
package main
