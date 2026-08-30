# LINE identity is the only identity

Users are identified solely by their LINE `userId`, verified from the LIFF ID token. There
are no usernames, no passwords, no password hashes, and no reset flow. A user row is created
on first open of the app.

The app's users are an elderly family sharing a household. A password is a thing they must
remember and type on a phone keyboard, and forgetting it makes a family member the support
desk. LINE is an app they are already permanently logged into, so tapping the LIFF entry is
the entire login. Hand-rolling credential storage would also add the riskiest possible code
to a project whose purpose is partly to learn Go.

## Consequences

The app cannot be opened in an ordinary browser — only inside LINE. Local development needs
an HTTPS tunnel and a separate development LIFF app. If browser access is ever needed, the
answer is LINE Login on the web, reusing the same identity, not a second credential system.
