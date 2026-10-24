import express from 'express';
import http from 'http';
import net from 'net';
import path from 'path';
import { fileURLToPath } from 'url';
import { spawn, execSync, type ChildProcess } from 'child_process';
import fs from 'fs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const PORT = 3000;
const GO_PORT = 8080;
const isProd = process.env.NODE_ENV === 'production';

// Ensure Go backend binary exists
const binPath = path.join(__dirname, 'bin', 'nodepanel');
if (!fs.existsSync(binPath)) {
  console.log('[bridge] Compiling Termux NodePanel Go backend binary...');
  try {
    fs.mkdirSync(path.join(__dirname, 'bin'), { recursive: true });
    execSync('go build -o bin/nodepanel ./cmd/panel', { stdio: 'inherit', cwd: __dirname });
    console.log('[bridge] Go backend compiled successfully.');
  } catch (err) {
    console.error('[bridge] Failed to compile Go binary:', err);
  }
}

// Spawn Go backend daemon
let goProcess: ChildProcess | null = null;
function startGoBackend() {
  console.log(`[bridge] Launching Go backend on 127.0.0.1:${GO_PORT}...`);
  goProcess = spawn(binPath, ['start', `--port=${GO_PORT}`, '--host=127.0.0.1', '--data=./data'], {
    cwd: __dirname,
    stdio: ['ignore', 'inherit', 'inherit'],
  });

  goProcess.on('exit', (code, signal) => {
    console.log(`[bridge] Go backend exited with code ${code} (${signal})`);
  });
}

startGoBackend();

// Graceful cleanup on server shutdown
const cleanup = () => {
  if (goProcess && goProcess.pid) {
    console.log('[bridge] Terminating Go backend process...');
    goProcess.kill('SIGTERM');
  }
  process.exit(0);
};
process.on('SIGINT', cleanup);
process.on('SIGTERM', cleanup);

async function createServer() {
  const app = express();
  const server = http.createServer(app);

  // Proxy /api requests to Go backend on 127.0.0.1:8080
  app.use('/api', (req, res) => {
    const proxyReq = http.request(
      {
        hostname: '127.0.0.1',
        port: GO_PORT,
        path: req.originalUrl,
        method: req.method,
        headers: {
          ...req.headers,
          host: `127.0.0.1:${GO_PORT}`,
        },
      },
      (proxyRes) => {
        res.writeHead(proxyRes.statusCode || 500, proxyRes.headers);
        proxyRes.pipe(res);
      }
    );

    proxyReq.on('error', (err) => {
      res.status(502).json({
        error: true,
        message: 'Backend Go service starting up...',
        details: err.message,
      });
    });

    req.pipe(proxyReq);
  });

  // Proxy WebSocket connections (/ws and /api/ws) to Go backend
  server.on('upgrade', (req, socket, head) => {
    const pathname = req.url ? req.url.split('?')[0] : '';
    if (pathname === '/ws' || pathname === '/api/ws') {
      const proxySocket = net.connect(GO_PORT, '127.0.0.1', () => {
        proxySocket.write(`${req.method} ${req.url} HTTP/${req.httpVersion}\r\n`);
        for (let i = 0; i < (req.rawHeaders?.length || 0); i += 2) {
          proxySocket.write(`${req.rawHeaders[i]}: ${req.rawHeaders[i + 1]}\r\n`);
        }
        proxySocket.write('\r\n');
        if (head && head.length > 0) {
          proxySocket.write(head);
        }
        socket.pipe(proxySocket);
        proxySocket.pipe(socket);
      });

      proxySocket.on('error', () => {
        socket.destroy();
      });
      socket.on('error', () => {
        proxySocket.destroy();
      });
    }
  });

  // Vite middleware in dev or static files in production
  if (!isProd) {
    const { createServer: createViteServer } = await import('vite');
    const vite = await createViteServer({
      server: { middlewareMode: true },
      appType: 'spa',
    });
    app.use(vite.middlewares);
  } else {
    app.use(express.static(path.join(__dirname, 'dist')));
    app.get('*', (_req, res) => {
      res.sendFile(path.join(__dirname, 'dist', 'index.html'));
    });
  }

  server.listen(PORT, '0.0.0.0', () => {
    console.log(`[bridge] Termux NodePanel Web Server running at http://0.0.0.0:${PORT}`);
  });
}

createServer().catch((err) => {
  console.error('[bridge] Failed to initialize server:', err);
  process.exit(1);
});
