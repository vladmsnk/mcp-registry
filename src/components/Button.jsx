export function Button({ variant = 'primary', children, onClick, disabled }) {
  return (
    <button className={`btn btn-${variant}`} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  );
}
