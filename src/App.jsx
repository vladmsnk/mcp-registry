import { useState, useEffect, useCallback } from 'react';
import { ServerList } from './components/ServerList';
import { ServerDetail } from './components/ServerDetail';
import { RegisterServer } from './components/RegisterServer';

export default function App() {
  const [screen, setScreen] = useState('list');
  const [servers, setServers] = useState([]);
  const [selectedServer, setSelectedServer] = useState(null);

  const fetchServers = useCallback(async () => {
    try {
      const res = await fetch('/api/servers');
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) setServers(data);
    } catch {
      // backend not running
    }
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

  const handleSelectServer = (server) => {
    setSelectedServer(server);
    setScreen('detail');
  };

  const handleDeleteServer = (serverId) => {
    setServers((prev) => prev.filter((s) => s.id !== serverId));
    setScreen('list');
    setSelectedServer(null);
  };

  const handleSyncServer = async () => {
    try {
      const res = await fetch('/api/servers');
      if (!res.ok) return;
      const data = await res.json();
      if (!Array.isArray(data)) return;
      setServers(data);
      if (selectedServer) {
        const updated = data.find((s) => s.id === selectedServer.id);
        if (updated) setSelectedServer(updated);
      }
    } catch {
      // ignore
    }
  };

  const renderScreen = () => {
    if (screen === 'register') {
      return <RegisterServer onBack={() => setScreen('list')} onSubmit={handleRegister} />;
    }
    if (screen === 'detail' && selectedServer) {
      return (
        <ServerDetail
          server={selectedServer}
          onBack={() => { setScreen('list'); setSelectedServer(null); }}
          onDelete={handleDeleteServer}
          onSync={handleSyncServer}
        />
      );
    }
    return (
      <ServerList
        servers={servers}
        onNavigateRegister={() => setScreen('register')}
        onSelectServer={handleSelectServer}
      />
    );
  };

  return (
    <div className="app-shell">
      {renderScreen()}
    </div>
  );
}
