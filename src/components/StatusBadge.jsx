export function StatusBadge({ active }) {
  return (
    <span className={`status ${active ? 'status-active' : 'status-inactive'}`}>
      <span className={`status-dot ${active ? 'dot-active' : 'dot-inactive'}`} />
      {active ? 'Active' : 'Inactive'}
    </span>
  );
}
