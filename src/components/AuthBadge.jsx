export function AuthBadge({ keycloakClientId }) {
  if (keycloakClientId) {
    return <span className="auth-badge auth-badge-active">OAuth</span>;
  }
  return <span className="auth-badge auth-badge-none">None</span>;
}
