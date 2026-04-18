<script setup>
import { ref, onMounted } from 'vue'

const status = ref({ status: 'offline', nodes_crawled: 0, db_connected: false })
const nodes = ref([])
const targetUrl = ref('')
const loading = ref(false)
const message = ref('')

const API_BASE = '/api'

const fetchStatus = async () => {
  try {
    const res = await fetch(`${API_BASE}/status`)
    status.value = await res.json()
  } catch (err) {
    console.error('Eroare la preluarea statusului:', err)
  }
}

const fetchNodes = async () => {
  try {
    const res = await fetch(`${API_BASE}/nodes`)
    nodes.value = await res.json()
  } catch (err) {
    console.error('Eroare la preluarea nodurilor:', err)
  }
}

const startCrawl = async () => {
  if (!targetUrl.value) return
  loading.value = true
  message.value = 'Se trimite cererea de scanare...'
  
  try {
    const res = await fetch(`${API_BASE}/crawl`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: targetUrl.value })
    })
    
    if (res.ok) {
      message.value = 'Scanarea a pornit in fundal!'
      targetUrl.value = ''
      setTimeout(() => { message.value = '' }, 3000)
    } else {
      message.value = 'Eroare la pornirea scanarii.'
    }
  } catch (err) {
    message.value = 'Eroare de conexiune la API.'
  } finally {
    loading.value = false
    fetchStatus()
  }
}

onMounted(() => {
  fetchStatus()
  fetchNodes()
  setInterval(() => {
    fetchStatus()
    fetchNodes()
  }, 5000)
})
</script>

<template>
  <div class="app-wrapper">
    <div class="container">
      <header>
        <div class="logo-area">
          <h1>🕷️ Onion Spider</h1>
          <p class="subtitle">Deep Web Explorer</p>
        </div>
        <div class="status-bar" :class="{ online: status.db_connected }">
          <span class="dot"></span>
          <span class="status-text">DB: {{ status.db_connected ? 'CONECTAT' : 'DECONECTAT' }}</span>
          <span class="divider">|</span>
          <span class="nodes-count">Noduri: {{ status.nodes_crawled }}</span>
        </div>
      </header>

      <main>
        <section class="crawl-form">
          <h2>Porneste o scanare noua</h2>
          <div class="input-group">
            <input 
              v-model="targetUrl" 
              type="url" 
              placeholder="Ex: http://xmh57jr...v3.onion" 
              @keyup.enter="startCrawl"
            />
            <button @click="startCrawl" :disabled="loading">
              <span v-if="loading">Se procesează...</span>
              <span v-else>Crawl 🚀</span>
            </button>
          </div>
          <p v-if="message" class="info-message">{{ message }}</p>
        </section>

        <section class="nodes-list">
          <div class="section-header">
            <h2>Site-uri descoperite</h2>
            <button class="btn-refresh" @click="fetchNodes">🔄</button>
          </div>
          <div class="table-responsive">
            <table>
              <thead>
                <tr>
                  <th class="col-id">ID</th>
                  <th>URL</th>
                  <th>Titlu</th>
                  <th class="col-server">Server</th>
                  <th class="col-status">Status</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="node in nodes" :key="node.id">
                  <td class="col-id">{{ node.id }}</td>
                  <td class="url">{{ node.url }}</td>
                  <td class="title">{{ node.title || 'Fără titlu' }}</td>
                  <td class="col-server">{{ node.server_header || '-' }}</td>
                  <td class="col-status"><span class="badge" :class="{ 's-200': node.status_code === 200 }">{{ node.status_code }}</span></td>
                </tr>
                <tr v-if="nodes.length === 0">
                  <td colspan="5" class="empty-state">Nu există date încă. Introdu un URL pentru a începe.</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </main>
    </div>
  </div>
</template>

<style>
/* Reset & Global */
body, html {
  margin: 0;
  padding: 0;
  background: #0f0f0f;
  color: #e0e0e0;
}
</style>

<style scoped>
.app-wrapper {
  min-height: 100vh;
  background: #0f0f0f;
  display: flex;
  justify-content: center;
}

.container {
  width: 100%;
  max-width: 100%; /* Full screen */
  padding: 20px 40px;
  box-sizing: border-box;
}

header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-bottom: 1px solid #333;
  padding-bottom: 20px;
  margin-bottom: 40px;
  flex-wrap: wrap;
  gap: 20px;
}

.logo-area h1 {
  color: #ff3333;
  margin: 0;
  font-size: 2.5rem;
  letter-spacing: -1px;
}

.subtitle {
  margin: 5px 0 0;
  color: #888;
  font-size: 0.9rem;
  text-transform: uppercase;
  letter-spacing: 2px;
}

.status-bar {
  background: #1a1a1a;
  padding: 10px 20px;
  border-radius: 50px;
  font-size: 0.9rem;
  display: flex;
  align-items: center;
  gap: 10px;
  border: 1px solid #333;
}

.dot {
  width: 8px;
  height: 8px;
  background: #ff3333;
  border-radius: 50%;
  box-shadow: 0 0 10px rgba(255, 51, 51, 0.5);
}

.status-bar.online .dot {
  background: #00ff00;
  box-shadow: 0 0 10px rgba(0, 255, 0, 0.5);
}

.divider { color: #444; }

/* Form Area */
.crawl-form {
  background: #161616;
  padding: 30px;
  border-radius: 12px;
  margin-bottom: 40px;
  border: 1px solid #222;
  box-shadow: 0 10px 30px rgba(0,0,0,0.5);
}

.crawl-form h2 {
  margin-top: 0;
  font-size: 1.2rem;
  margin-bottom: 20px;
  color: #bbb;
}

.input-group {
  display: flex;
  gap: 15px;
}

input {
  flex: 1;
  padding: 15px 20px;
  border-radius: 8px;
  border: 1px solid #333;
  background: #0a0a0a;
  color: #fff;
  font-size: 1rem;
  transition: border-color 0.3s;
}

input:focus {
  outline: none;
  border-color: #ff3333;
}

button {
  padding: 0 40px;
  background: #ff3333;
  border: none;
  color: white;
  font-weight: bold;
  border-radius: 8px;
  cursor: pointer;
  font-size: 1rem;
  transition: background 0.3s, transform 0.1s;
}

button:hover:not(:disabled) {
  background: #cc0000;
  transform: translateY(-2px);
}

button:active:not(:disabled) {
  transform: translateY(0);
}

button:disabled {
  background: #444;
  cursor: not-allowed;
}

.info-message {
  margin-top: 15px;
  color: #ffaa00;
  font-size: 0.9rem;
}

/* Nodes List */
.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.btn-refresh {
  background: transparent;
  border: 1px solid #333;
  padding: 8px;
  font-size: 1.2rem;
  border-radius: 50%;
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.table-responsive {
  width: 100%;
  overflow-x: auto;
  background: #161616;
  border-radius: 12px;
  border: 1px solid #222;
}

table {
  width: 100%;
  border-collapse: collapse;
  min-width: 800px;
}

th, td {
  padding: 18px 20px;
  text-align: left;
  border-bottom: 1px solid #222;
}

th {
  background: #1a1a1a;
  color: #888;
  font-weight: 600;
  font-size: 0.85rem;
  text-transform: uppercase;
  letter-spacing: 1px;
}

tr:hover td {
  background: #1d1d1d;
}

.url {
  font-family: 'Courier New', Courier, monospace;
  color: #4da6ff;
  font-size: 0.9rem;
  max-width: 300px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.title {
  font-weight: 500;
}

.badge {
  background: #444;
  padding: 4px 10px;
  border-radius: 4px;
  font-size: 0.8rem;
  font-weight: bold;
}

.badge.s-200 {
  background: #008000;
}

.empty-state {
  padding: 40px;
  text-align: center;
  color: #666;
}

/* Responsive Mobile */
@media (max-width: 768px) {
  .container {
    padding: 15px;
  }
  
  header {
    flex-direction: column;
    align-items: flex-start;
    gap: 15px;
  }

  .logo-area h1 {
    font-size: 1.8rem;
  }

  .input-group {
    flex-direction: column;
  }

  button {
    padding: 15px;
  }

  .col-server, .col-id {
    display: none;
  }

  table {
    min-width: 100%;
  }

  .url {
    max-width: 150px;
  }
}
</style>
