import { useState } from 'react';
import { Button } from './Button';
import { InputField } from './InputField';
import { SelectField } from './SelectField';

const AUTH_OPTIONS = ['OAuth 2.1', 'API Key', 'None'];

const INITIAL_FORM = {
  name: '',
  endpoint: '',
  description: '',
  owner: '',
  authType: 'OAuth 2.1',
  tags: '',
};

export function RegisterServer({ onBack, onSubmit }) {
  const [form, setForm] = useState(INITIAL_FORM);

  const update = (field) => (value) => setForm((prev) => ({ ...prev, [field]: value }));

  const handleSubmit = () => {
    if (!form.name || !form.endpoint) return;

    onSubmit({
      name: form.name,
      endpoint: form.endpoint,
      description: form.description,
      owner: form.owner,
      authType: form.authType,
      tags: form.tags
        .split(',')
        .map((t) => t.trim())
        .filter(Boolean),
    });

    setForm(INITIAL_FORM);
  };

  return (
    <div className="screen">
      <div className="screen-label">Register Server</div>
      <div className="card">
        <div className="card-header register-header">
          <div className="header-left">
            <button className="back-btn" onClick={onBack}>
              <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
                <path d="M13 4L7 10L13 16" stroke="#374151" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
            <h1 className="card-title register-title">Register New Server</h1>
          </div>
        </div>

        <div className="form-container">
          <div className="form">
            <InputField label="Server Name" placeholder="e.g. Code Search" value={form.name} onChange={update('name')} />
            <InputField label="Endpoint URL" placeholder="https://" value={form.endpoint} onChange={update('endpoint')} />
            <InputField label="Description" placeholder="Describe what this server does..." type="textarea" value={form.description} onChange={update('description')} />

            <div className="form-row">
              <div className="form-field-owner">
                <InputField label="Owner / Team" placeholder="e.g. Platform Team" value={form.owner} onChange={update('owner')} />
              </div>
              <div className="form-field-auth">
                <SelectField label="Auth Type" value={form.authType} onChange={update('authType')} options={AUTH_OPTIONS} />
              </div>
            </div>

            <InputField label="Tags" placeholder="Add tags separated by commas..." value={form.tags} onChange={update('tags')} />

            <div className="form-divider" />

            <div className="form-actions">
              <Button variant="secondary" onClick={onBack}>Cancel</Button>
              <Button onClick={handleSubmit}>Register Server</Button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
