<script setup>
import { ref, onMounted, watch, nextTick } from 'vue'
import { Network } from 'vis-network'

const status = ref({ status: 'offline', nodes_crawled: 0, pending_nodes: 0, db_connected: false, active_workers: 0 })
const nodes = ref([])
const edges = ref([])
const targetUrl = ref('')
const searchQuery = ref('')
const loading = ref(false)
const isSearching = ref(false)
const isGraphView = ref(false)
const message = ref('')

const graphContainer = ref(null)
let network = null

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
  if (isSearching.value) return
  try {
    const res = await fetch(`${API_BASE}/nodes`)
    nodes.value = await res.json() || []
  } catch (err) {
    console.error('Eroare la preluarea nodurilor:', err)
  }
}

const fetchEdges = async () => {
  try {
    const res = await fetch(`${API_BASE}/edges`)
    edges.value = await res.json() || []
  } catch (err) {
    console.error('Eroare la preluarea legaturilor:', err)
  }
}

const performSearch = async () => {
  if (!searchQuery.value.trim()) {
    isSearching.value = false
    fetchNodes()
    return
  }
  
  isSearching.value = true
  try {
    const res = await fetch(`${API_BASE}/search?q=${encodeURIComponent(searchQuery.value)}`)
    if (res.ok) {
      nodes.value = await res.json() || []
    }
  } catch (err) {
    console.error('Eroare la cautare:', err)
  }
}

const startCrawl = async () => {
  if (!targetUrl.value) return
  loading.value = true
  message.value = 'Se adauga in coada...'
  
  try {
    const res = await fetch(`${API_BASE}/crawl`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: targetUrl.value })
    })
    
    if (res.ok) {
      message.value = 'URL adaugat cu succes in coada de scanare!'
      targetUrl.value = ''
      setTimeout(() => { message.value = '' }, 3000)
    } else {
      message.value = 'Eroare la adaugarea in coada.'
    }
  } catch (err) {
    message.value = 'Eroare de conexiune la API.'
  } finally {
    loading.value = false
    fetchStatus()
  }
}

const drawGraph = () => {
  if (!graphContainer.value) return

  // Cream nodurile pentru retea, folosim URL-ul ca ID unic
  const visNodes = nodes.value.map(n => {
    let color = '#4da6ff' // Default albastru
    if (n.status_code === 200) color = '#00ff00' // Verde pentru succes
    else if (n.status_code === 400 || n.status_code === 429) color = '#ffcc00' // Galben pt rate limit/bad req
    else if (n.status_code === 0 && n.processing_status === 'completed') color = '#ff3333' // Rosu pentru inaccesibil
    else if (n.processing_status === 'pending_v2') color = '#888888' // Gri pt coada

    return {
      id: n.url,
      label: n.title ? (n.title.length > 20 ? n.title.substring(0, 20) + '...' : n.title) : 'Sursa Secundara',
      title: n.url, // Tooltip pe hover
      color: { background: color, border: '#222' },
      shape: 'dot',
      size: n.status_code === 200 ? 20 : 10
    }
  })

  // Evitam nodurile duplicat
  const uniqueNodes = Array.from(new Map(visNodes.map(item => [item.id, item])).values())

  // Cream muchiile
  const visEdges = edges.value.map(e => ({
    from: e.source,
    to: e.target,
    arrows: 'to',
    color: { color: '#444', highlight: '#ff3333' }
  }))

  const data = { nodes: uniqueNodes, edges: visEdges }
  const options = {
    nodes: { font: { color: '#e0e0e0', size: 12 } },
    edges: { smooth: { type: 'continuous' } },
    physics: { 
      barnesHut: { gravitationalConstant: -2000, centralGravity: 0.3, springLength: 95 },
      stabilization: { iterations: 150 }
    },
    interaction: { hover: true, tooltipDelay: 100 }
  }

  if (network) {
    network.destroy()
  }
  network = new Network(graphContainer.value, data, options)
}

watch(isGraphView, async (newVal) => {
  if (newVal) {
    await fetchEdges()
    await fetchNodes()
    await nextTick()
    drawGraph()
  }
})

onMounted(() => {
  fetchStatus()
  fetchNodes()
  setInterval(() => {
    fetchStatus()
    if (!isGraphView.value) fetchNodes() // Update doar in list view
  }, 5000)
})
</script>

<template>
  <div class="app-wrapper">
    <div class="container">
      <header>
        <div class="logo-area">
          <h1>🕷️ Onion Spider</h1>
          <p class="subtitle">Recursive Deep Web Explorer & Search Engine</p>
        </div>
        <div class="status-bar" :class="{ online: status.db_connected }">
          <span class="dot"></span>
          <span class="status-text">DB: {{ status.db_connected ? 'OK' : 'OFF' }}</span>
          <span class="divider">|</span>
          <span class="nodes-count">Scanate: {{ status.nodes_crawled }}</span>
          <span class="divider">|</span>
          <span class="pending-count">In Coada: {{ status.pending_nodes }}</span>
          <span class="divider">|</span>
          <span class="workers-count">Workeri: {{ status.active_workers }}</span>
        </div>
      </header>

      <main>
        <section class="crawl-form">
          <h2>Adauga URL la coada de scanare</h2>
          <div class="input-group">
            <input 
              v-model="targetUrl" 
              type="url" 
              placeholder="Ex: http://xmh57jr...v3.onion" 
              @keyup.enter="startCrawl"
            />
            <button @click="startCrawl" :disabled="loading">
              <span v-if="loading">Trimitere...</span>
              <span v-else>Adauga 🚀</span>
            </button>
          </div>
          <p v-if="message" class="info-message">{{ message }}</p>
        </section>

        <div class="view-controls">
          <button class="toggle-btn" :class="{ active: !isGraphView }" @click="isGraphView = false">📄 Listă / Căutare</button>
          <button class="toggle-btn" :class="{ active: isGraphView }" @click="isGraphView = true">🕸️ Grafic Rețea</button>
        </div>

        <!-- Vizualizarea Lista + Cautare -->
        <div v-if="!isGraphView">
          <section class="search-form">
            <div class="input-group search-group">
              <span class="search-icon">🔍</span>
              <input 
                v-model="searchQuery" 
                type="text" 
                placeholder="Cauta in textul paginilor scanate..." 
                @keyup.enter="performSearch"
                @input="performSearch"
              />
            </div>
          </section>

          <section class="nodes-list">
            <div class="section-header">
              <h2>{{ isSearching ? 'Rezultatele Cautarii' : 'Ultimele Site-uri Descoperite' }}</h2>
              <button class="btn-refresh" @click="fetchNodes" v-if="!isSearching">🔄</button>
              <button class="btn-refresh" @click="searchQuery = ''; performSearch()" v-if="isSearching">❌</button>
            </div>

            <div class="table-responsive">
              <table>
                <thead>
                  <tr>
                    <th class="col-id">ID</th>
                    <th>URL</th>
                    <th>Titlu / Status Procesare</th>
                    <th class="col-server">Server</th>
                    <th class="col-status">Cod</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="node in nodes" :key="node.id">
                    <td class="col-id">{{ node.id }}</td>
                    <td class="url">{{ node.url }}</td>
                    <td>
                      <div class="title-row">
                        <span class="title">{{ node.title || 'In asteptare...' }}</span>
                        <span class="status-pill" :class="node.processing_status">
                          {{ node.processing_status }}
                        </span>
                      </div>
                    </td>
                    <td class="col-server">{{ node.server_header || '-' }}</td>
                    <td class="col-status">
                      <span class="badge" :class="{ 's-200': node.status_code === 200 }">
                        {{ node.status_code || '-' }}
                      </span>
                    </td>
                  </tr>
                  <tr v-if="nodes.length === 0">
                    <td colspan="5" class="empty-state">Nu există date încă. Introdu un URL pentru a începe.</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        </div>

        <!-- Vizualizarea Grafica -->
        <div v-if="isGraphView" class="graph-section">
          <div class="section-header">
            <h2>Harta Interactivă a Rețelei</h2>
            <button class="btn-refresh" @click="drawGraph">🔄 Reîncarcă</button>
          </div>
          <div class="legend">
            <span class="legend-item"><span class="dot-legend l-green"></span> 200 OK</span>
            <span class="legend-item"><span class="dot-legend l-yellow"></span> 4xx Eroare Server</span>
            <span class="legend-item"><span class="dot-legend l-red"></span> Offline / SOCKS Fail</span>
            <span class="legend-item"><span class="dot-legend l-gray"></span> În Coadă</span>
          </div>
          <div ref="graphContainer" class="graph-container"></div>
        </div>
      </main>
    </div>
  </div>
</template>

<style>
/* Reset & Global */
body, html {
  margin: 0;
  padding: 0;
  background: #0a0a0a;
  color: #e0e0e0;
  font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
}
</style>

<style scoped>
.app-wrapper {
  min-height: 100vh;
  background: #0a0a0a;
  display: flex;
  justify-content: center;
}

.container {
  width: 100%;
  padding: 20px 40px;
  box-sizing: border-box;
}

header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-bottom: 1px solid #222;
  padding-bottom: 25px;
  margin-bottom: 40px;
  flex-wrap: wrap;
  gap: 20px;
}

.logo-area h1 {
  color: #ff3333;
  margin: 0;
  font-size: 2.2rem;
  font-weight: 800;
  letter-spacing: -1.5px;
}

.subtitle {
  margin: 2px 0 0;
  color: #555;
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 3px;
  font-weight: 600;
}

.status-bar {
  background: #151515;
  padding: 12px 25px;
  border-radius: 12px;
  font-size: 0.85rem;
  display: flex;
  align-items: center;
  gap: 15px;
  border: 1px solid #222;
  color: #aaa;
}

.dot {
  width: 10px;
  height: 10px;
  background: #ff3333;
  border-radius: 50%;
}

.status-bar.online .dot {
  background: #00ff00;
  box-shadow: 0 0 10px rgba(0, 255, 0, 0.4);
}

.divider { color: #333; font-weight: 100; }

.crawl-form {
  background: #111;
  padding: 30px;
  border-radius: 16px;
  margin-bottom: 20px;
  border: 1px solid #1a1a1a;
  box-shadow: 0 4px 20px rgba(0,0,0,0.3);
}

.crawl-form h2 {
  margin-top: 0;
  font-size: 1.1rem;
  margin-bottom: 20px;
  color: #777;
}

.input-group {
  display: flex;
  gap: 12px;
}

input {
  flex: 1;
  padding: 16px 20px;
  border-radius: 10px;
  border: 1px solid #222;
  background: #050505;
  color: #fff;
  font-size: 1rem;
}

.search-group { margin-bottom: 30px; position: relative; }
.search-icon { position: absolute; left: 15px; top: 18px; font-size: 1.2rem; }
.search-group input { padding-left: 45px; background: #151515; border-color: #333; }
.search-group input:focus { border-color: #ff3333; }

button {
  padding: 0 35px;
  background: #ff3333;
  border: none;
  color: white;
  font-weight: 700;
  border-radius: 10px;
  cursor: pointer;
}

.view-controls {
  display: flex;
  gap: 15px;
  margin-bottom: 30px;
  justify-content: center;
}

.toggle-btn {
  background: #222;
  color: #888;
  padding: 12px 25px;
  border-radius: 30px;
  transition: all 0.3s;
}

.toggle-btn.active {
  background: #ff3333;
  color: white;
  box-shadow: 0 4px 15px rgba(255, 51, 51, 0.4);
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 15px;
}

.btn-refresh {
  background: transparent;
  border: 1px solid #333;
  color: #aaa;
  padding: 8px 15px;
  border-radius: 8px;
}

.graph-section {
  background: #111;
  padding: 20px;
  border-radius: 16px;
  border: 1px solid #1a1a1a;
}

.graph-container {
  width: 100%;
  height: 650px;
  background: #0a0a0a;
  border: 1px solid #222;
  border-radius: 12px;
  margin-top: 15px;
}

.legend {
  display: flex;
  gap: 20px;
  padding: 10px;
  background: #161616;
  border-radius: 8px;
  font-size: 0.85rem;
}

.legend-item { display: flex; align-items: center; gap: 8px; color: #888; }
.dot-legend { width: 12px; height: 12px; border-radius: 50%; display: inline-block; }
.l-green { background: #00ff00; }
.l-yellow { background: #ffcc00; }
.l-red { background: #ff3333; }
.l-gray { background: #888888; }

.status-pill {
  font-size: 0.7rem;
  padding: 2px 8px;
  border-radius: 10px;
  text-transform: uppercase;
  font-weight: 800;
  background: #222;
  color: #888;
}

.status-pill.pending_v2 { color: #ffcc00; background: rgba(255, 204, 0, 0.1); }
.status-pill.crawling { color: #4da6ff; background: rgba(77, 166, 255, 0.1); animation: pulse 2s infinite; }
.status-pill.completed { color: #00ff00; background: rgba(0, 255, 0, 0.1); }
.status-pill.failed { color: #ff3333; background: rgba(255, 51, 51, 0.1); }

@keyframes pulse {
  0% { opacity: 1; }
  50% { opacity: 0.5; }
  100% { opacity: 1; }
}

.title-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

table {
  width: 100%;
  border-collapse: collapse;
}

th, td {
  padding: 15px 20px;
  text-align: left;
  border-bottom: 1px solid #151515;
}

th {
  background: #0d0d0d;
  color: #444;
  font-weight: 700;
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 1.5px;
}

.url {
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
  color: #4da6ff;
  font-size: 0.85rem;
  max-width: 250px;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 768px) {
  .container { padding: 15px; }
  header { flex-direction: column; align-items: flex-start; }
  .input-group { flex-direction: column; }
  .col-server, .col-id { display: none; }
  .legend { flex-wrap: wrap; }
}
</style>
