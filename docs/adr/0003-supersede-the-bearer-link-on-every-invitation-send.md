# Supersede the bearer link on every invitation send

Nightloch stores only the SHA-256 hash of a Partner invitation's bearer token, so no
process can reproduce a link it has already emailed. A resend must therefore either mint a
fresh token or keep a second live credential for the same invitation, and the second option
widens the blast radius of the very thing hashing exists to contain. Each send and resend
consequently mints a new token, replaces the stored hash, and supersedes the previous link;
exactly one live bearer link exists per invitation at any time, and the reminder says so.

This is a property of the credential, not of the invitation. The invited address, the
proposed Log sharing, and the seven-day expiry are untouched, which is what S3-66 means by
resending repeating the same invitation. The cost is real and is accepted: an invitee who
kept only the earlier email finds its link dead. They are not stranded — a signed-in User
answers any invitation addressed to their verified email from **Partner invitations**
(S3-82) — and an invitee who cannot find either email is the case a resend exists for.
