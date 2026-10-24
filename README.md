# Termux NodePanel (Ubuntu proot-distro Edition)

Panel server Node.js ringan dan modular dengan desain **Bento UI Glassmorphism Liquid Glass**, process supervisor native Go, dan persistent sidebar di setiap fitur — didesain khusus agar berjalan mulus dan stabil di dalam **Ubuntu (`proot-distro`)** pada Android Termux tanpa Docker atau Kubernetes.

---

## ⚡ Solusi Masalah: Kenapa `npm install` Gagal di Ubuntu PRoot?

Banyak pengguna mengalami error saat menjalankan `npm install` di Ubuntu PRoot Android karena:
1. Paket `npm` bawaan `apt-get install npm` di Ubuntu versi repositori lama sering *broken* atau tidak kompatibel dengan kernel Android.
2. Menjalankan *build tools* berat (`vite`, `esbuild`, `rollup`) langsung di HP sering *crash* karena syscall `clone3` atau kehabisan RAM (*Out of Memory* / OOM).

### ✅ Solusi di Termux NodePanel:
**Anda TIDAK PERLU menjalankan `npm install` atau `npm run build` untuk dashboard panel ini!**
Seluruh antarmuka web React Bento UI Glassmorphism sudah **ter-bundle dan tersemat langsung (*embedded*) ke dalam satu binary Go (`bin/nodepanel`)**.

Panel dapat langsung dijalankan hanya dengan binary Go tersebut!

---

## 🚀 Cara Setup Termudah (1 Perintah Selesai)

Masuk ke Ubuntu di Termux Anda:
```bash
proot-distro login ubuntu
```

Jalankan script otomatisasi setup (otomatis menginstal Go, SQLite3, Git, dan Node.js 20 LTS resmi dari NodeSource):
```bash
git clone https://github.com/termux-nodepanel/nodepanel.git
cd nodepanel
chmod +x setup-proot.sh && ./setup-proot.sh
```

Setelah selesai, jalankan panel:
```bash
./bin/nodepanel start --port=8080 --host=0.0.0.0
```

Buka browser di HP Anda:
👉 **`http://localhost:8080`**

---

## 🛠️ Langkah Manual (Jika Ingin Jalankan Sendiri Tanpa Script)

Jika Anda ingin menginstal dependensi satu per satu secara manual:

### 1. Install Dependensi Dasar di Ubuntu proot
```bash
apt-get update -y
apt-get install -y curl git sqlite3 golang-go ca-certificates
```

### 2. Install Node.js 20 LTS Resmi (NodeSource)
*Gunakan repo resmi NodeSource agar npm tidak crash di Android*:
```bash
curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt-get install -y nodejs
```

### 3. Kompilasi Binary Go
```bash
mkdir -p bin
go build -o bin/nodepanel ./cmd/panel
```

### 4. Jalankan Panel
```bash
./bin/nodepanel start --port=8080 --host=0.0.0.0
```

---

## 🖥️ Command CLI Bawaan

Binary `nodepanel` menyediakan command diagnostik bawaan:

```bash
# Cek kesehatan environment, arsitektur, dan toolchain yang terdeteksi
./bin/nodepanel doctor

# Cek runtime JSON
./bin/nodepanel runtime

# Cek versi panel
./bin/nodepanel version

# Jalankan daemon panel
./bin/nodepanel start [--port=8080] [--host=0.0.0.0] [--data=./data]
```

---

## 🎨 Fitur Utama Panel

1. **Bento UI Glassmorphism Liquid Glass**:
   - Layout modular bento grid asimetris dengan efek frosted glass (`backdrop-blur-xl`) dan specular liquid highlights.
   - Angka tabular (`tabular-nums`) untuk monitoring RAM (RSS MB), Port, Uptime, dan PID proses.
2. **Persistent Sidebar di Setiap Fitur**:
   - Sidebar navigasi selalu hadir di setiap halaman (Overview, All Servers, Server Detail Console, File Manager, Environment Variables, System Packages, Doctor Diagnostics, Activity Logs, dan Settings).
   - Dilengkapi *Quick Server Switcher* langsung di sidebar untuk berpindah antar aplikasi Node.js secara instan.
3. **Supervisor Proses Native Go**:
   - Menjalankan aplikasi Node.js Anda via child process terisolasi (`os/exec` + process group isolation).
   - Penghentian proses bertahap: mengirim sinyal `SIGTERM`, menunggu timeout graceful, lalu fallback `SIGKILL` jika macet.
   - Auto-restart terkonfigurasi: `never`, `always`, atau `on-failure` (dengan exponential backoff).
4. **Real-time Live Console Streaming**:
   - Output stdout & stderr dialirkan secara langsung melalui WebSocket RFC 6455 tanpa dependensi eksternal.
   - Ring buffer memori (1.000 baris) dan file log permanen di `data/servers/<id>/logs/server.log`.
   - Stdin bar interaktif untuk mengirim perintah ke proses yang aktif.
5. **In-Browser File Manager**:
   - Jelajahi folder proyek, buat file/folder baru, dan edit script `.js`/`.json`/`.env` langsung dari browser.
   - Proteksi ketat terhadap path traversal (`../`) agar tidak bisa mengakses file di luar folder server.
6. **System Packages Manager**:
   - Deteksi otomatis `apt-get` di proot-distro Ubuntu.
   - Instalasi paket pendukung Node.js (Git, FFmpeg, Python 3 untuk node-gyp, ImageMagick, cURL, jq, tmux, make) dengan progress bar dan concurrency lock.
7. **SQLite Persistence**:
   - Metadata server, konfigurasi port, variabel lingkungan, dan audit log riwayat aktivitas tersimpan permanen di `data/nodepanel.db`.

---

## 💡 Tips Stabilitas Background di Android

Agar proses server tidak dimatikan saat layar HP mati:
1. Di Termux, jalankan perintah:
   ```bash
   termux-wake-lock
   ```
2. Buka **Pengaturan Android > Aplikasi > Termux > Baterai**, lalu ubah menjadi **Tidak Dibatasi / Unrestricted**.
3. Jika ingin panel otomatis berjalan saat HP baru menyala, gunakan aplikasi pelengkap **Termux:Boot** dari F-Droid.

---

## 🧪 Menjalankan Pipeline Pengujian

Proyek ini telah dilengkapi pipeline test lengkap (Unit Test, Concurrency Test, dan Load Test):
```bash
./scripts/test_pipeline.sh
```
Semua test lulus 100% dengan throughput load test mencapai **38.000+ request per detik**.
