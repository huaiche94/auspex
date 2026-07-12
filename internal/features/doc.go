// Package features derives prediction-input signals from prompts,
// repositories, sessions, and Progress Trees (ADD §14.2).
//
// Privacy boundary (Constitution §7 rule 2, CONTRACT_FREEZE.md "Privacy
// contract"): raw prompt text enters this package through exactly one
// function, ExtractPromptFeatures, and never leaves it. No exported type
// in this package carries raw prompt text, a substring of it, or any
// reversible encoding of it — only derived scalars, booleans, and a
// SHA-256 digest. Any change that adds a field capable of holding raw
// prompt text is a contract violation, not a feature.
package features
