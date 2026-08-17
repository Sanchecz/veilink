# Security policy

Veilink v0.x has not received an independent security audit. Treat it as a
release candidate until that review is complete. Production operators should
use the supplied hardening, isolate the VDS, patch Go/Caddy/OS dependencies, and
test recovery before onboarding users.

Report vulnerabilities privately to the repository owner. Include the affected
version/commit, deployment topology, reproduction steps, impact, and a proposed
mitigation if available. Do not include live tokens, private addresses, or user
traffic captures.

Supported versions: only the most recent tagged minor release receives security
fixes. Compromised tokens must be revoked immediately; they cannot be recovered
or safely reused.
