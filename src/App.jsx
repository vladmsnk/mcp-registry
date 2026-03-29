import { useState, useEffect, useCallback } from 'react';
import { ServerList } from './components/ServerList';
import { RegisterServer } from './components/RegisterServer';

export default function App() {
  const [screen, setScreen] = useState('list');
  const [servers, setServers] = useState([]);

  const fetchServers = useCallback(async () => {
    const res = await fetch('/api/servers');
    const data = await res.json();
    setServers(data);
  }, []);

  useEffect(() => {
    fetchServers();
  }, [fetchServers]);

  const handleRegister = async (serverData) => {
    const res = await fetch('/api/servers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(serverData),
    });

    if (res.ok) {
      await fetchServers();
      setScreen('list');
    }
  };

  if (screen === 'register') {
    return <RegisterServer onBack={() => setScreen('list')} onSubmit={handleRegister} />;
  }

  return <ServerList servers={servers} onNavigateRegister={() => setScreen('register')} />;
}
