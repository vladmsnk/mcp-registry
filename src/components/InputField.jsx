export function InputField({ label, placeholder, value, onChange, type = 'text', error, hint }) {
  return (
    <div className="form-field">
      <label className="form-label">{label}</label>
      {type === 'textarea' ? (
        <textarea
          className={`form-input form-textarea ${error ? 'form-input-error' : ''}`}
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      ) : (
        <input
          type={type}
          className={`form-input ${error ? 'form-input-error' : ''}`}
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      {error && <span className="form-error">{error}</span>}
      {hint && !error && <span className="form-hint">{hint}</span>}
    </div>
  );
}
