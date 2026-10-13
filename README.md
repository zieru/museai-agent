# Muse AI Gateway (Go Edition)

High-performance, lightweight AI Gateway yang menjembatani **Meta Muse AI (`muse.ai`)** ke standar **OpenAI Chat Completions API (`/api/v1/chat/completions`)** dan **Anthropic Messages API (`/api/v1/messages`)** dengan fitur:

1. **Lightweight Browser**: Didukung oleh `chromedp` murni (native Chrome DevTools Protocol) tanpa Playwright atau Python. Dikonfigurasi headless (`--headless=new`) dan mematikan render gambar (`disable_images: true`) untuk menghemat RAM (~50-80MB per worker).
2. **Isolated Session**: Profil browser, cache, cookies, dan LocalStorage terisolasi per akun di direktori `./sessions/<account_id>`.
3. **Multiple Accounts**: Manajemen multi-akun dengan rotasi otomatis (*round-robin*) atau routing langsung via model name (e.g. `muse/muse_01`) atau header `X-Account-ID`.
4. **Multimodal Support (Vision / Image)**: Menerima payload OpenAI multimodal `image_url` (Base64 data URL maupun URL remote), menyimpannya secara temporer, dan mengunggahnya secara headless ke input upload Muse via CDP `DOM.setFileInputFiles`.
5. **Claude Code Harness**: Porting tool parser code fence / markdown card ke Anthropic `tool_use` events (`Write`, `Bash`, dll.).

---

## Panduan Penggunaan

### 1. Login Akun (Interactive One-Time Setup)
Jalankan perintah ini untuk membuka browser (headed) sekali saja dan login ke akun Muse AI Anda:
```bash
# Login akun pertama (muse_01)
.\musegateway.exe -login muse_01

# Login akun kedua (muse_02)
.\musegateway.exe -login muse_02
```
Setelah login di jendela browser yang terbuka dan membuka thread chat, kembali ke terminal lalu tekan **ENTER**. Session state akan otomatis tersimpan di folder `./sessions/<account_id>`.

---

### 2. Konfigurasi (`configs/config.yaml`)
Anda dapat mengatur port, timeout, dan mendaftarkan akun di file `configs/config.yaml`:
```yaml
port: 8765
api_key: ""
default_model: "muse/muse_01"
headless: true
disable_images: true
timeout_sec: 300

accounts:
  - id: "muse_01"
    name: "Primary Muse Account"
    chat_url: "https://muse.ai"
    storage_dir: "sessions/muse_01"
    enabled: true
    proxy: ""
  - id: "muse_02"
    name: "Secondary Muse Account"
    chat_url: "https://muse.ai"
    storage_dir: "sessions/muse_02"
    enabled: true
    proxy: ""
```

---

### 3. Menjalankan Gateway Daemon
```bash
.\musegateway.exe
```

Gateway akan aktif di `http://127.0.0.1:8765`.

---

### 4. Contoh Pemanggilan API

#### A. Cek Daftar Model (`/api/v1/models`)
```bash
curl http://127.0.0.1:8765/api/v1/models
```

#### B. OpenAI Chat Completions (Model: `muse/muse_01`)
```bash
curl http://127.0.0.1:8765/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "muse/muse_01",
    "messages": [
      {"role": "user", "content": "Halo, jelaskan apa itu Go goroutine"}
    ]
  }'
```

#### C. Multimodal / Vision (Image Base64 / URL)
```bash
curl http://127.0.0.1:8765/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "muse/muse_01",
    "messages": [
      {
        "role": "user",
        "content": [
          {"type": "text", "text": "Jelaskan gambar ini"},
          {
            "type": "image_url",
            "image_url": {"url": "data:image/png;base64,iVBORw..."}
          }
        ]
      }
    ]
  }'
```

#### D. Claude Code Integration
Set environment variable Claude Code:
```bash
# PowerShell
$env:ANTHROPIC_BASE_URL="http://127.0.0.1:8765/api/v1"
$env:ANTHROPIC_API_KEY="dummy"
claude
```
