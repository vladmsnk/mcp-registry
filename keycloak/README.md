# Keycloak realm

`realm-export.template.json` is rendered by Keycloak at import time using
`${env.VAR}` substitution. Every secret (client secret, redirect URIs, web
origins, demo user password) is sourced from environment variables — see
`../.env.example` for the full list.

## Service-account permissions (NHI #5)

The `mcp-hub` service account currently holds `realm-management:manage-clients`,
which is broader than the hub strictly needs. Defence-in-depth runs in code:
the `keycloak.AdminClient` rejects any operation against a client whose
`clientId` does not start with `mcp-server-` (see `internal/keycloak/admin.go`).

To tighten further, migrate to **fine-grained admin permissions** (FGAP) so the
SA can only mutate clients matching `mcp-server-*`:

1. Start Keycloak with `--features=admin-fine-grained-authz`.
2. In the admin console, open the `realm-management` client → **Permissions**.
3. Enable client-scope permissions for `clients` and create a *Client policy*
   that matches `clientId` against the regex `^mcp-server-.+$`.
4. Replace the SA's `manage-clients` grant with that scoped permission.

This change cannot be expressed cleanly in realm-export JSON — apply it via
the admin REST API or `kc.sh` import after the realm is created.

## Demo user

The `testuser` block exists for the e2e test (`../e2e_test.sh`) and the
shipped demo flow. **Remove it before deploying to any non-dev environment**
or set `MCP_TEST_USER_PASSWORD` to a strong secret and disable interactive
login on that user.

## Admin MFA (NHI #11)

The `mcp-admin` realm role auto-enrolls users into TOTP via the
`requiredActions` block (`CONFIGURE_TOTP`). On first login an admin must
register an authenticator app; subsequent logins prompt for the OTP. Pair
this with a Keycloak browser flow that requires OTP for any user holding
`mcp-admin` to make the requirement non-bypassable.
