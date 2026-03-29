export function InputField({ label, placeholder, value, onChange, type = 'text' }) {
  return (
    <div className="form-field">
      <label className="form-label">{label}</label>
      {type === 'textarea' ? (
        <textarea
          className="form-input form-textarea"
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      ) : (
        <input
          type={type}
          className="form-input"
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </div>
  );
}
