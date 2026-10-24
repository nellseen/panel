import React, { useState, useEffect, useRef } from 'react';
import {
  ServerModel,
  LogLine,
  FileEntry,
  AutoRestartPolicy,
} from '../types';
import {
  Play,
  Square,
  RotateCw,
  Terminal,
  FolderTree,
  KeyRound,
  GitBranch,
  Settings,
  Download,
  Trash2,
  Save,
  Plus,
  Send,
  Eye,
  EyeOff,
  FileCode,
  ArrowLeft,
  Loader2,
  Clock,
  HardDrive,
} from 'lucide-react';
import {
  fetchServer,
  fetchServerLogs,
  fetchServerFiles,
  readServerFile,
  writeServerFile,
  deleteServerFile,
  updateServer,
  installServerDeps,
  wsClient,
} from '../services/api';

interface ServerDetailViewProps {
  serverId: string;
  onBack: () => void;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onRestart: (id: string) => void;
  onDelete: (id: string) => void;
}

type DetailTab = 'console' | 'files' | 'env' | 'git' | 'settings';

export const ServerDetailView: React.FC<ServerDetailViewProps> = ({
  serverId,
  onBack,
  onStart,
  onStop,
  onRestart,
  onDelete,
}) => {
  const [server, setServer] = useState<ServerModel | null>(null);
  const [activeTab, setActiveTab] = useState<DetailTab>('console');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Console state
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [autoScroll, setAutoScroll] = useState(true);
  const [stdinInput, setStdinInput] = useState('');
  const consoleBottomRef = useRef<HTMLDivElement>(null);

  // Files state
  const [currentPath, setCurrentPath] = useState('');
  const [fileList, setFileList] = useState<FileEntry[]>([]);
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [fileContent, setFileContent] = useState('');
  const [isSavingFile, setIsSavingFile] = useState(false);
  const [newFileName, setNewFileName] = useState('');
  const [showNewFileInput, setShowNewFileInput] = useState(false);

  // Env vars state
  const [envVars, setEnvVars] = useState<Record<string, string>>({});
  const [newKey, setNewKey] = useState('');
  const [newVal, setNewVal] = useState('');
  const [revealSecrets, setRevealSecrets] = useState(false);
  const [envSaved, setEnvSaved] = useState(false);

  // Settings state
  const [cfgName, setCfgName] = useState('');
  const [cfgDesc, setCfgDesc] = useState('');
  const [cfgPort, setCfgPort] = useState(3001);
  const [cfgEntry, setCfgEntry] = useState('index.js');
  const [cfgAutoRestart, setCfgAutoRestart] = useState<AutoRestartPolicy>('never');
  const [isSavingCfg, setIsSavingCfg] = useState(false);

  // Install deps state
  const [isInstalling, setIsInstalling] = useState(false);
  const [installOutput, setInstallOutput] = useState('');

  const loadData = async () => {
    try {
      setLoading(true);
      const srv = await fetchServer(serverId);
      setServer(srv);
      setEnvVars(srv.env_vars || {});
      setCfgName(srv.name);
      setCfgDesc(srv.description || '');
      setCfgPort(srv.port);
      setCfgEntry(srv.entry_file);
      setCfgAutoRestart(srv.auto_restart);

      const initialLogs = await fetchServerLogs(serverId);
      setLogs(initialLogs || []);
    } catch (err: any) {
      setError(err.message || 'Failed to load server');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();

    wsClient.subscribeServer(serverId);

    const unsubLog = wsClient.on('log', (payload: any) => {
      if (payload.server_id === serverId) {
        setLogs((prev) => [
          ...prev,
          {
            timestamp: payload.timestamp || new Date().toISOString(),
            stream: payload.stream || 'stdout',
            text: payload.text || '',
          },
        ]);
      }
    });

    const unsubStatus = wsClient.on('status', (payload: any) => {
      if (payload.server_id === serverId) {
        setServer((prev) =>
          prev
            ? { ...prev, status: payload.new_status, last_exit_code: payload.exit_code }
            : null
        );
      }
    });

    return () => {
      wsClient.unsubscribeServer(serverId);
      unsubLog();
      unsubStatus();
    };
  }, [serverId]);

  useEffect(() => {
    if (autoScroll && activeTab === 'console') {
      consoleBottomRef.current?.scrollIntoView({ behavior: 'smooth' });
    }
  }, [logs, autoScroll, activeTab]);

  useEffect(() => {
    if (activeTab === 'files') {
      loadFiles(currentPath);
    }
  }, [activeTab, currentPath]);

  const loadFiles = async (path: string) => {
    try {
      const files = await fetchServerFiles(serverId, path);
      setFileList(files);
    } catch (err: any) {
      console.error(err);
    }
  };

  const handleOpenFile = async (file: FileEntry) => {
    if (file.is_dir) {
      setCurrentPath(file.path);
      setSelectedFile(null);
    } else {
      try {
        const content = await readServerFile(serverId, file.path);
        setSelectedFile(file.path);
        setFileContent(content);
      } catch (err: any) {
        alert('Failed to read file: ' + err.message);
      }
    }
  };

  const handleSaveFile = async () => {
    if (!selectedFile) return;
    try {
      setIsSavingFile(true);
      await writeServerFile(serverId, selectedFile, fileContent);
      alert('File saved successfully.');
    } catch (err: any) {
      alert('Failed to save file: ' + err.message);
    } finally {
      setIsSavingFile(false);
    }
  };

  const handleCreateFile = async () => {
    if (!newFileName.trim()) return;
    const target = currentPath ? `${currentPath}/${newFileName.trim()}` : newFileName.trim();
    try {
      await writeServerFile(serverId, target, '');
      setNewFileName('');
      setShowNewFileInput(false);
      loadFiles(currentPath);
      setSelectedFile(target);
      setFileContent('');
    } catch (err: any) {
      alert('Failed to create file: ' + err.message);
    }
  };

  const handleDeleteFile = async (filePath: string) => {
    if (!confirm(`Delete ${filePath}?`)) return;
    try {
      await deleteServerFile(serverId, filePath);
      if (selectedFile === filePath) {
        setSelectedFile(null);
        setFileContent('');
      }
      loadFiles(currentPath);
    } catch (err: any) {
      alert('Failed to delete file: ' + err.message);
    }
  };

  const handleSendStdin = (e: React.FormEvent) => {
    e.preventDefault();
    if (!stdinInput.trim()) return;
    wsClient.sendStdin(serverId, stdinInput);
    setStdinInput('');
  };

  const handleAddEnv = () => {
    if (!newKey.trim()) return;
    setEnvVars((prev) => ({ ...prev, [newKey.trim()]: newVal }));
    setNewKey('');
    setNewVal('');
    setEnvSaved(false);
  };

  const handleRemoveEnv = (key: string) => {
    setEnvVars((prev) => {
      const copy = { ...prev };
      delete copy[key];
      return copy;
    });
    setEnvSaved(false);
  };

  const handleSaveEnv = async () => {
    try {
      await updateServer(serverId, { env_vars: envVars });
      setEnvSaved(true);
      setTimeout(() => setEnvSaved(false), 3000);
    } catch (err: any) {
      alert('Failed to save environment variables: ' + err.message);
    }
  };

  const handleSaveConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      setIsSavingCfg(true);
      await updateServer(serverId, {
        name: cfgName,
        description: cfgDesc,
        port: cfgPort,
        entry_file: cfgEntry,
        auto_restart: cfgAutoRestart,
      });
      alert('Server configuration updated.');
    } catch (err: any) {
      alert('Failed to update config: ' + err.message);
    } finally {
      setIsSavingCfg(false);
    }
  };

  const handleRunNpmInstall = async () => {
    try {
      setIsInstalling(true);
      setInstallOutput('Running npm install on Termux/Linux host...\n');
      const res = await installServerDeps(serverId);
      setInstallOutput((prev) => prev + res.output);
    } catch (err: any) {
      setInstallOutput((prev) => prev + `\nError: ${err.message}`);
    } finally {
      setIsInstalling(false);
    }
  };

  if (loading && !server) {
    return (
      <div className="glass-panel p-12 rounded-2xl text-center text-slate-400">
        <Loader2 className="w-8 h-8 animate-spin mx-auto mb-2 text-emerald-400" />
        <p className="text-xs">Loading instance details...</p>
      </div>
    );
  }

  if (error || !server) {
    return (
      <div className="glass-panel p-6 rounded-2xl space-y-4">
        <div className="text-rose-400 font-semibold text-sm">Failed to load server: {error}</div>
        <button
          onClick={onBack}
          className="px-3 py-1.5 bg-white/10 text-slate-200 text-xs rounded-xl"
        >
          Back to Server List
        </button>
      </div>
    );
  }

  const isRunning = server.status === 'running';

  return (
    <div className="space-y-6">
      {/* Bento Header Glass Card */}
      <div className="glass-panel glass-specular rounded-2xl p-6 flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <button
            onClick={onBack}
            className="p-2 text-slate-400 hover:text-white bg-white/[0.04] border border-white/[0.08] rounded-xl hover:bg-white/[0.08] transition-colors"
            title="Back to Servers"
          >
            <ArrowLeft className="w-4 h-4" />
          </button>

          <div>
            <div className="flex items-center gap-2.5 flex-wrap">
              <h2 className="text-xl font-bold text-white tracking-tight">{server.name}</h2>
              <span className="text-xs font-mono text-slate-400">
                <span>Port</span> <strong className="text-emerald-300">:{server.port}</strong>
              </span>
              <span className="text-xs text-slate-500 font-mono">·</span>
              <span className="text-xs font-mono text-slate-300 capitalize flex items-center gap-1.5">
                <span
                  className={`w-2 h-2 rounded-full ${
                    isRunning
                      ? 'bg-emerald-400 shadow-[0_0_8px_rgba(16,185,129,0.8)]'
                      : server.status === 'crashed'
                      ? 'bg-rose-400'
                      : 'bg-slate-500'
                  }`}
                />
                <span>{server.status}</span>
              </span>
            </div>

            <div className="text-xs text-slate-400 flex items-center gap-3 mt-1.5 font-mono">
              <span className="text-slate-400">{server.id}</span>
              <span>·</span>
              <span>
                Entry: <code className="text-slate-200">{server.entry_file}</code>
              </span>
              {server.metrics && isRunning && (
                <>
                  <span>·</span>
                  <span>
                    RAM: <strong className="text-emerald-400">{server.metrics.memory_mb.toFixed(1)} MB</strong>
                  </span>
                  <span>·</span>
                  <span>
                    PID: <strong className="text-cyan-400">{server.metrics.pid}</strong>
                  </span>
                </>
              )}
            </div>
          </div>
        </div>

        {/* Action Controls */}
        <div className="flex items-center gap-2 flex-wrap">
          {isRunning ? (
            <>
              <button
                onClick={() => onStop(server.id)}
                className="flex items-center gap-1.5 bg-rose-500/15 hover:bg-rose-500/25 text-rose-300 border border-rose-500/30 px-3.5 py-1.5 rounded-xl text-xs font-semibold transition-colors"
              >
                <Square className="w-3.5 h-3.5" />
                <span>Stop</span>
              </button>
              <button
                onClick={() => onRestart(server.id)}
                className="flex items-center gap-1.5 bg-amber-500/15 hover:bg-amber-500/25 text-amber-300 border border-amber-500/30 px-3 py-1.5 rounded-xl text-xs font-semibold transition-colors"
              >
                <RotateCw className="w-3.5 h-3.5" />
                <span>Restart</span>
              </button>
            </>
          ) : (
            <button
              onClick={() => onStart(server.id)}
              className="flex items-center gap-1.5 bg-gradient-to-r from-emerald-500 to-teal-500 hover:from-emerald-400 hover:to-teal-400 text-slate-950 px-4 py-1.5 rounded-xl text-xs font-bold shadow-[0_0_15px_rgba(16,185,129,0.3)] transition-colors"
            >
              <Play className="w-3.5 h-3.5 fill-current" />
              <span>Start Server</span>
            </button>
          )}

          <button
            onClick={handleRunNpmInstall}
            disabled={isInstalling}
            className="flex items-center gap-1.5 bg-white/[0.04] hover:bg-white/[0.08] text-slate-200 border border-white/[0.08] px-3 py-1.5 rounded-xl text-xs font-medium transition-colors disabled:opacity-50"
            title="Execute npm install in server directory"
          >
            <Download className="w-3.5 h-3.5 text-cyan-400" />
            <span>{isInstalling ? 'Installing...' : 'npm install'}</span>
          </button>

          <button
            onClick={() => {
              if (confirm(`Are you sure you want to delete server ${server.name}?`)) {
                onDelete(server.id);
              }
            }}
            className="p-2 text-slate-500 hover:text-rose-400 hover:bg-rose-500/10 rounded-xl transition-colors"
            title="Delete Server"
          >
            <Trash2 className="w-4 h-4" />
          </button>
        </div>
      </div>

      {/* Clean Segmented Tab Bar */}
      <div className="glass-panel p-1 rounded-2xl flex items-center gap-1 overflow-x-auto">
        {[
          { id: 'console' as DetailTab, label: 'Live Console', icon: Terminal },
          { id: 'files' as DetailTab, label: 'File Manager', icon: FolderTree },
          { id: 'env' as DetailTab, label: 'Environment Variables', icon: KeyRound },
          { id: 'git' as DetailTab, label: 'Git Settings', icon: GitBranch },
          { id: 'settings' as DetailTab, label: 'Configuration', icon: Settings },
        ].map((tab) => {
          const Icon = tab.icon;
          const isActive = activeTab === tab.id;

          return (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={`flex items-center gap-2 px-3.5 py-2 rounded-xl text-xs font-medium transition-all whitespace-nowrap ${
                isActive
                  ? 'bg-white/10 text-emerald-300 font-semibold shadow-sm'
                  : 'text-slate-400 hover:text-slate-200'
              }`}
            >
              <Icon className="w-3.5 h-3.5" />
              <span>{tab.label}</span>
            </button>
          );
        })}
      </div>

      {/* Tab 1: Live Console Bento Box */}
      {activeTab === 'console' && (
        <div className="space-y-3">
          <div className="glass-panel rounded-2xl overflow-hidden flex flex-col h-[520px]">
            {/* Terminal Header Bar */}
            <div className="bg-black/30 px-4 py-2.5 border-b border-white/[0.06] flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="w-2.5 h-2.5 rounded-full bg-rose-500/80 inline-block" />
                <span className="w-2.5 h-2.5 rounded-full bg-amber-500/80 inline-block" />
                <span className="w-2.5 h-2.5 rounded-full bg-emerald-500/80 inline-block" />
                <span className="font-mono text-xs text-slate-400 ml-2">stdout / stderr stream</span>
              </div>
              <div className="flex items-center gap-2">
                <button
                  onClick={() => setAutoScroll(!autoScroll)}
                  className={`text-[11px] px-2.5 py-1 rounded-lg border transition-colors ${
                    autoScroll
                      ? 'bg-emerald-500/15 text-emerald-300 border-emerald-500/30'
                      : 'bg-white/[0.04] text-slate-400 border-white/[0.06]'
                  }`}
                >
                  Auto-scroll: {autoScroll ? 'ON' : 'OFF'}
                </button>
                <button
                  onClick={() => setLogs([])}
                  className="text-[11px] px-2.5 py-1 rounded-lg bg-white/[0.04] hover:bg-white/[0.08] text-slate-300 border border-white/[0.06]"
                >
                  Clear
                </button>
              </div>
            </div>

            {/* Terminal Body */}
            <div className="flex-1 p-4 overflow-y-auto font-mono text-xs text-slate-200 space-y-1 bg-black/40">
              {logs.length === 0 ? (
                <div className="text-slate-600 italic py-8 text-center">
                  [No console output generated yet. Start the process or issue a command.]
                </div>
              ) : (
                logs.map((line, idx) => {
                  let colorClass = 'text-slate-300';
                  if (line.stream === 'stderr') colorClass = 'text-rose-400';
                  else if (line.stream === 'system') colorClass = 'text-emerald-400';

                  return (
                    <div key={idx} className="flex items-start gap-2 leading-relaxed break-all">
                      <span className="text-slate-600 text-[10px] select-none shrink-0">
                        {new Date(line.timestamp).toLocaleTimeString([], { hour12: false })}
                      </span>
                      <span className="text-slate-600 select-none text-[10px]">[{line.stream}]</span>
                      <span className={colorClass}>{line.text}</span>
                    </div>
                  );
                })
              )}
              <div ref={consoleBottomRef} />
            </div>

            {/* Stdin Bar */}
            <form
              onSubmit={handleSendStdin}
              className="p-3 bg-black/30 border-t border-white/[0.06] flex items-center gap-2"
            >
              <span className="text-emerald-400 font-mono text-xs pl-2">&gt;</span>
              <input
                type="text"
                placeholder={isRunning ? 'Send command to process stdin...' : 'Process is stopped'}
                disabled={!isRunning}
                value={stdinInput}
                onChange={(e) => setStdinInput(e.target.value)}
                className="flex-1 bg-transparent border-none text-xs text-slate-100 placeholder-slate-600 focus:outline-none font-mono"
              />
              <button
                type="submit"
                disabled={!isRunning || !stdinInput.trim()}
                className="p-1.5 text-slate-400 hover:text-emerald-400 disabled:opacity-30 transition-colors"
                title="Send Stdin"
              >
                <Send className="w-4 h-4" />
              </button>
            </form>
          </div>

          {installOutput && (
            <div className="glass-panel rounded-2xl p-4 space-y-2">
              <div className="flex items-center justify-between text-xs font-semibold text-slate-300">
                <span>npm install output</span>
                <button
                  onClick={() => setInstallOutput('')}
                  className="text-slate-500 hover:text-slate-300"
                >
                  Close
                </button>
              </div>
              <pre className="bg-black/30 p-3 rounded-xl font-mono text-xs text-slate-300 max-h-48 overflow-y-auto whitespace-pre-wrap">
                {installOutput}
              </pre>
            </div>
          )}
        </div>
      )}

      {/* Tab 2: File Manager Bento Grid */}
      {activeTab === 'files' && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-5 h-[550px]">
          {/* File Explorer Tree */}
          <div className="glass-panel rounded-2xl p-4 flex flex-col justify-between overflow-hidden">
            <div>
              <div className="flex items-center justify-between mb-3 pb-2 border-b border-white/[0.06]">
                <div className="text-xs font-semibold text-slate-200 flex items-center gap-1.5 truncate">
                  <FolderTree className="w-3.5 h-3.5 text-emerald-400 shrink-0" />
                  <span className="truncate">/{currentPath || 'root'}</span>
                </div>
                <div className="flex items-center gap-1">
                  {currentPath && (
                    <button
                      onClick={() => {
                        const parts = currentPath.split('/');
                        parts.pop();
                        setCurrentPath(parts.join('/'));
                      }}
                      className="p-1 text-slate-400 hover:text-slate-200 text-xs rounded-lg hover:bg-white/[0.06]"
                    >
                      ..
                    </button>
                  )}
                  <button
                    onClick={() => setShowNewFileInput(!showNewFileInput)}
                    className="p-1 text-emerald-400 hover:bg-emerald-500/10 rounded-lg transition-colors"
                    title="Create File"
                  >
                    <Plus className="w-3.5 h-3.5" />
                  </button>
                </div>
              </div>

              {showNewFileInput && (
                <div className="mb-3 flex items-center gap-1.5">
                  <input
                    type="text"
                    placeholder="filename.js"
                    value={newFileName}
                    onChange={(e) => setNewFileName(e.target.value)}
                    className="flex-1 bg-black/40 border border-white/[0.1] rounded-lg px-2.5 py-1 text-xs text-slate-200 focus:outline-none"
                  />
                  <button
                    onClick={handleCreateFile}
                    className="px-2.5 py-1 bg-emerald-500 text-slate-950 font-semibold text-xs rounded-lg"
                  >
                    Add
                  </button>
                </div>
              )}

              {/* Files List */}
              <div className="overflow-y-auto max-h-[440px] space-y-1">
                {fileList.map((f) => (
                  <div
                    key={f.path}
                    className={`flex items-center justify-between px-3 py-2 rounded-xl text-xs cursor-pointer transition-colors ${
                      selectedFile === f.path
                        ? 'bg-emerald-500/15 text-emerald-300 font-semibold border border-emerald-500/30'
                        : 'text-slate-300 hover:bg-white/[0.04]'
                    }`}
                  >
                    <div
                      onClick={() => handleOpenFile(f)}
                      className="flex items-center gap-2 truncate flex-1"
                    >
                      {f.is_dir ? (
                        <FolderTree className="w-3.5 h-3.5 text-amber-400 shrink-0" />
                      ) : (
                        <FileCode className="w-3.5 h-3.5 text-cyan-400 shrink-0" />
                      )}
                      <span className="truncate">{f.name}</span>
                    </div>

                    {!f.is_dir && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          handleDeleteFile(f.path);
                        }}
                        className="p-1 text-slate-500 hover:text-rose-400 transition-colors"
                        title="Delete file"
                      >
                        <Trash2 className="w-3 h-3" />
                      </button>
                    )}
                  </div>
                ))}
              </div>
            </div>
          </div>

          {/* In-Browser Code Editor */}
          <div className="md:col-span-2 glass-panel rounded-2xl flex flex-col overflow-hidden">
            <div className="bg-black/30 px-4 py-2.5 border-b border-white/[0.06] flex items-center justify-between">
              <span className="font-mono text-xs text-slate-300 truncate">
                {selectedFile ? selectedFile : 'No file open'}
              </span>
              {selectedFile && (
                <button
                  onClick={handleSaveFile}
                  disabled={isSavingFile}
                  className="flex items-center gap-1.5 px-3 py-1 bg-emerald-500 hover:bg-emerald-400 text-slate-950 font-semibold text-xs rounded-xl shadow-sm transition-colors"
                >
                  <Save className="w-3.5 h-3.5" />
                  <span>{isSavingFile ? 'Saving...' : 'Save File'}</span>
                </button>
              )}
            </div>

            <div className="flex-1 p-2 bg-black/40">
              {selectedFile ? (
                <textarea
                  value={fileContent}
                  onChange={(e) => setFileContent(e.target.value)}
                  className="w-full h-full bg-transparent text-slate-100 font-mono text-xs p-3 focus:outline-none resize-none"
                  spellCheck={false}
                />
              ) : (
                <div className="h-full flex items-center justify-center text-slate-600 text-xs italic">
                  Select a file from the explorer on the left to view and edit
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* Tab 3: Environment Variables */}
      {activeTab === 'env' && (
        <div className="glass-panel rounded-2xl p-6 space-y-4 max-w-3xl">
          <div className="flex items-center justify-between">
            <div>
              <h3 className="font-bold text-white text-base">Environment Variables</h3>
              <p className="text-xs text-slate-400">
                Variables passed into <code className="text-slate-300">process.env</code> upon server startup
              </p>
            </div>
            <button
              onClick={() => setRevealSecrets(!revealSecrets)}
              className="flex items-center gap-1.5 text-xs text-slate-400 hover:text-slate-200"
            >
              {revealSecrets ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
              <span>{revealSecrets ? 'Hide values' : 'Reveal values'}</span>
            </button>
          </div>

          <div className="space-y-2">
            {Object.entries(envVars).map(([key, val]) => (
              <div
                key={key}
                className="flex items-center gap-2 bg-black/30 p-2.5 rounded-xl border border-white/[0.04] font-mono text-xs"
              >
                <span className="font-bold text-emerald-400 w-1/3 truncate">{key}</span>
                <span className="text-slate-500">=</span>
                <span className="flex-1 text-slate-300 truncate">
                  {revealSecrets ? val : '••••••••••••••••'}
                </span>
                <button
                  onClick={() => handleRemoveEnv(key)}
                  className="p-1 text-slate-500 hover:text-rose-400"
                  title="Remove"
                >
                  <Trash2 className="w-3.5 h-3.5" />
                </button>
              </div>
            ))}
          </div>

          <div className="flex items-center gap-2 pt-2 border-t border-white/[0.06]">
            <input
              type="text"
              placeholder="KEY_NAME"
              value={newKey}
              onChange={(e) => setNewKey(e.target.value.toUpperCase())}
              className="w-1/3 bg-black/30 border border-white/[0.08] rounded-xl px-3 py-2 text-xs text-slate-100 placeholder-slate-600 font-mono"
            />
            <input
              type="text"
              placeholder="value"
              value={newVal}
              onChange={(e) => setNewVal(e.target.value)}
              className="flex-1 bg-black/30 border border-white/[0.08] rounded-xl px-3 py-2 text-xs text-slate-100 placeholder-slate-600 font-mono"
            />
            <button
              onClick={handleAddEnv}
              className="px-3.5 py-2 bg-white/[0.06] hover:bg-white/[0.1] text-slate-200 text-xs font-semibold rounded-xl"
            >
              Add
            </button>
          </div>

          <div className="flex items-center justify-between pt-4 border-t border-white/[0.06]">
            <span className="text-xs text-slate-500">
              Changes take effect on the next start/restart.
            </span>
            <button
              onClick={handleSaveEnv}
              className="px-4 py-2 bg-emerald-500 hover:bg-emerald-400 text-slate-950 font-bold text-xs rounded-xl shadow-md transition-colors"
            >
              {envSaved ? 'Saved!' : 'Save Variables'}
            </button>
          </div>
        </div>
      )}

      {/* Tab 4: Git Settings */}
      {activeTab === 'git' && (
        <div className="glass-panel rounded-2xl p-6 space-y-4 max-w-2xl">
          <h3 className="font-bold text-white text-base">Git Repository Association</h3>
          <p className="text-xs text-slate-400 leading-relaxed">
            Local repository synchronization and branch management.
          </p>

          <div className="bg-black/30 p-4 rounded-xl border border-white/[0.06] space-y-2 text-xs font-mono">
            <div>
              <span className="text-slate-500 block text-[10px]">REPOSITORY ORIGIN</span>
              <span className="text-slate-200">{server.repo_url || 'No remote repository attached'}</span>
            </div>
            <div>
              <span className="text-slate-500 block text-[10px]">LOCAL WORKING DIRECTORY</span>
              <span className="text-emerald-400">data/servers/{server.id}</span>
            </div>
          </div>
        </div>
      )}

      {/* Tab 5: Configuration */}
      {activeTab === 'settings' && (
        <form onSubmit={handleSaveConfig} className="glass-panel rounded-2xl p-6 space-y-4 max-w-xl">
          <h3 className="font-bold text-white text-base">Lifecycle Configuration</h3>

          <div>
            <label className="block text-xs font-medium text-slate-300 mb-1">Server Name</label>
            <input
              type="text"
              value={cfgName}
              onChange={(e) => setCfgName(e.target.value)}
              className="w-full bg-black/30 border border-white/[0.08] rounded-xl px-3 py-2 text-xs text-slate-100"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium text-slate-300 mb-1">Assigned Port</label>
              <input
                type="number"
                value={cfgPort}
                onChange={(e) => setCfgPort(parseInt(e.target.value) || 3001)}
                className="w-full bg-black/30 border border-white/[0.08] rounded-xl px-3 py-2 text-xs text-slate-100 font-mono"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-300 mb-1">Auto-Restart Policy</label>
              <select
                value={cfgAutoRestart}
                onChange={(e) => setCfgAutoRestart(e.target.value as AutoRestartPolicy)}
                className="w-full bg-black/30 border border-white/[0.08] rounded-xl px-3 py-2 text-xs text-slate-100"
              >
                <option value="never">Never</option>
                <option value="on-failure">On-Failure</option>
                <option value="always">Always</option>
              </select>
            </div>
          </div>

          <div>
            <label className="block text-xs font-medium text-slate-300 mb-1">Entry Script</label>
            <input
              type="text"
              value={cfgEntry}
              onChange={(e) => setCfgEntry(e.target.value)}
              className="w-full bg-black/30 border border-white/[0.08] rounded-xl px-3 py-2 text-xs text-slate-100 font-mono"
            />
          </div>

          <div>
            <label className="block text-xs font-medium text-slate-300 mb-1">Description</label>
            <textarea
              rows={2}
              value={cfgDesc}
              onChange={(e) => setCfgDesc(e.target.value)}
              className="w-full bg-black/30 border border-white/[0.08] rounded-xl px-3 py-2 text-xs text-slate-100"
            />
          </div>

          <div className="pt-2">
            <button
              type="submit"
              disabled={isSavingCfg}
              className="px-4 py-2 bg-emerald-500 hover:bg-emerald-400 text-slate-950 font-bold text-xs rounded-xl shadow-md transition-colors"
            >
              {isSavingCfg ? 'Saving...' : 'Save Configuration'}
            </button>
          </div>
        </form>
      )}
    </div>
  );
};
