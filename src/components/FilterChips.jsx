const FILTERS = ['All', 'Active', 'Inactive'];

export function FilterChips({ active, onChange }) {
  return (
    <div className="filter-chips">
      {FILTERS.map((label) => (
        <button
          key={label}
          className={`chip ${active === label ? 'chip-active' : 'chip-inactive'}`}
          onClick={() => onChange(label)}
        >
          {label}
        </button>
      ))}
    </div>
  );
}
