/**
 * Shared UI status tone. The development concept fixtures that used to live in
 * this module were removed with their unreachable concept screens; only the
 * canonical tone type remains and it is re-exported by the status vocabulary.
 */
export type StatusTone = "good" | "info" | "warn" | "bad" | "neutral"
