export function UserHeader({ user }) {
  return (
    <div className="user-header">
      <span className="user-name">{user.preferred_username}</span>
      <a href="/auth/logout" className="btn btn-secondary btn-small">Sign out</a>
    </div>
  );
}
