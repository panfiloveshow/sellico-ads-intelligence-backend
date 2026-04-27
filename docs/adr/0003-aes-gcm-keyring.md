# ADR-0003: AES-256-GCM with versioned keyring for WB token encryption

- **Status**: Accepted
- **Date**: 2026-04-27 (Sprint 4 of v1.0 roadmap)
- **Authors**: backend team
- **Deciders**: tech lead, security

## Context

`seller_cabinets.encrypted_token` stores the user's WB API token. Loss or
theft of the encryption key = catastrophic (attacker can sync any tenant
into their own Sellico account, read campaign data, modify bids).

Previous design: single `ENCRYPTION_KEY` env var, AES-256-GCM with
random nonce, base64-encoded ciphertext (no version prefix). Worked but:
- No way to rotate the key without writing a custom script
- No way to introduce a new cipher (e.g. AES-SIV) without breaking old
  ciphertext

## Decision

Introduce a `Keyring` type holding `map[int][]byte` (version → key bytes).
Wire format becomes `v<N>:<base64-payload>`; legacy unversioned ciphertext
is still accepted by `Decrypt()` and `DecryptWithKeyring()` — they just
treat it as version 0 and try the supplied key (or every key in the ring).

A new `cmd/rotate-encryption-key` re-encrypts every row from any older
version onto the latest, in batched transactions. Operator workflow is
documented in `docs/deployment/key-rotation.md`.

## Alternatives considered

| Option | Pros | Cons | Why rejected |
|--------|------|------|--------------|
| Versioned keyring (chosen) | Backward compatible; trivial rotation; no migration of existing data required up-front | Two code paths (Decrypt vs DecryptWithKeyring) for transition window | Acceptable; the legacy path is a one-line wrapper |
| Single key, regenerate on rotation | Trivial code | Requires downtime: re-encrypt all data while no service is running | Customer-visible outage every rotation is a non-starter |
| Envelope encryption (KMS-managed DEK per row) | True per-row keys; auditable in KMS | KMS round-trip per encrypt/decrypt; vendor lock; new infra | Overkill for current threat model |
| Key in HashiCorp Vault | Centralised secret; audit trail | New infra to operate; SLA risk | No operational team to run Vault yet |

## Consequences

- **Good**: rotation now takes ~30 minutes with zero downtime. Compromise
  recovery story: "rotate the key" rather than "schedule maintenance".
- **Good**: explicit version tag means we'll never have to guess what
  cipher / key produced a given ciphertext.
- **Bad**: two code paths exist for the legacy-ciphertext case until all
  rows are re-encrypted. Mitigation: rotation tool is idempotent and the
  legacy fallback fires at most O(K) attempts where K = keyring size.

## How we'll know it worked

- After a rotation drill on staging, all rows show `v2:` prefix and can be
  decrypted by both api and worker without errors.
- The first real rotation completes in < 1 hour with `failed=0` reported.
- `crypto.ErrKeyVersionUnknown` never fires in production logs (would
  indicate a missing key, requiring immediate page).

## Links

- File: `internal/pkg/crypto/aes.go`
- File: `cmd/rotate-encryption-key/main.go`
- Doc: `docs/deployment/key-rotation.md`
