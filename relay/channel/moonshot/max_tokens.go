package moonshot

// kimiK3ContextWindowTokens is the K3 family's 1M-token context window. The
// output budget may be lowered to whatever remains of it, and a larger request
// is the official 400 rather than a silent clamp.
const kimiK3ContextWindowTokens uint = 1024 * 1024
