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
const physicsEnabled = ref(true)
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

const togglePhysics = () => {
  if (!network) return
  physicsEnabled.value = !physicsEnabled.value
  network.setOptions({ physics: physicsEnabled.value })
}

const drawGraph = () => {
  if (!graphContainer.value) return

  // Calculam gradele (numarul de legaturi pentru a mari nodurile principale)
  const nodeDegrees = {}
  edges.value.forEach(e => {
    nodeDegrees[e.source] = (nodeDegrees[e.source] || 0) + 1
    nodeDegrees[e.target] = (nodeDegrees[e.target] || 0) + 1
  })

  // Cream nodurile
  const visNodes = nodes.value.map(n => {
    let color = '#4da6ff' // Default albastru
    if (n.status_code === 200) color = '#00ff00' // Verde pentru succes
    else if (n.status_code === 400 || n.status_code === 429) color = '#ffcc00' // Galben pt erori
    else if (n.status_code === 0 && n.processing_status === 'completed') color = '#ff3333' // Rosu pentru inaccesibil
    else if (n.processing_status === 'pending_v2') color = '#888888' // Gri pt in coada

    const degree = nodeDegrees[n.url] || 0
    let size = 15 + (degree * 2.5) // Hub-urile cresc mult mai repede
    if (size > 60) size = 60 // Cap size crescut

    const cleanTitle = n.title ? n.title : 'Sursa Inaccesibila / Secundara'
    const shortLabel = cleanTitle.length > 25 ? cleanTitle.substring(0, 25) + '...' : cleanTitle

    const tooltipText = `${cleanTitle}\nURL: ${n.url}\nStatus HTTP: ${n.status_code} | Legaturi (Hub): ${degree}\n(Dublu click pe bila pentru a copia link-ul)`

    return {
      id: n.url,
      label: shortLabel,
      title: tooltipText,
      color: { 
        background: color, 
        border: '#000',
        highlight: { background: '#ffffff', border: color },
        hover: { background: color, border: '#ffffff' }
      },
      shape: 'dot',
      size: size,
      borderWidth: size > 30 ? 3 : 1,
      font: { color: '#ccc', size: size > 30 ? 16 : 10, face: 'Inter' },
      shadow: { enabled: true, color: 'rgba(0,0,0,0.6)', size: 10, x: 0, y: 0 }
    }
  })

  const uniqueNodes = Array.from(new Map(visNodes.map(item => [item.id, item])).values())

  // Cream muchiile transparente
  const visEdges = edges.value.map(e => ({
    from: e.source,
    to: e.target,
    arrows: { to: { enabled: true, scaleFactor: 0.4 } },
    color: { color: 'rgba(80, 80, 80, 0.4)', highlight: '#ff3333', hover: '#ffffff' },
    smooth: { type: 'dynamic' }
  }))

  const data = { nodes: uniqueNodes, edges: visEdges }
  const options = {
    edges: { width: 1, selectionWidth: 4 },
    physics: { 
      solver: 'forceAtlas2Based',
      forceAtlas2Based: {
        gravitationalConstant: -150,
        centralGravity: 0.015,
        springLength: 200,
        springConstant: 0.06,
        damping: 0.7
      },
      maxVelocity: 50,
      minVelocity: 0.1,
      stabilization: { iterations: 250 }
    },
    interaction: { 
      hover: true, 
      tooltipDelay: 50, 
      zoomView: true, 
      dragView: true,
      hideEdgesOnDrag: true // Pentru performanta cand tragem de ecran
    }
  }

  if (network) {
    network.destroy()
  }
  network = new Network(graphContainer.value, data, options)
  physicsEnabled.value = true

  network.on("stabilizationIterationsDone", function () {
    network.setOptions({ physics: false })
    physicsEnabled.value = false
  })

  // Actiune de Dublu Click pentru a copia URL-ul
  network.on("doubleClick", function (params) {
    if (params.nodes.length > 0) {
      const clickedUrl = params.nodes[0]
      navigator.clipboard.writeText(clickedUrl)
      alert("URL copiat în clipboard: \n" + clickedUrl)
    }
  })
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
    if (!isGraphView.value) fetchNodes() // Actualizam lista doar daca nu suntem in graph
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
            <div class="graph-actions">
              <button class="btn-action" @click="togglePhysics" :class="{ off: !physicsEnabled }">
                {{ physicsEnabled ? '❄️ Îngheață Mișcarea' : '🔥 Pornește Fizica' }}
              </button>
              <button class="btn-action primary" @click="drawGraph">🔄 Reîncarcă DB</button>
            </div>
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
/* Global */
body, html { margin: 0; padding: 0; background: #0a0a0a; color: #e0e0e0; font-family: 'Inter', system-ui, sans-serif; }

/* Vis.js Tooltip fix */
div.vis-tooltip {
  position: absolute;
  padding: 10px 15px !important;
  background-color: rgba(20, 20, 20, 0.95) !important;
  color: #ffffff !important;
  border: 1px solid #ff3333 !important;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.9);
  border-radius: 8px;
  pointer-events: none;
  z-index: 1000;
  font-family: 'Courier New', Courier, monospace !important;
  font-size: 13px !important;
  line-height: 1.5;
  white-space: pre-wrap;
}
</style>

<style scoped>
.app-wrapper { min-height: 100vh; display: flex; justify-content: center; }
.container { width: 100%; padding: 20px 40px; box-sizing: border-box; }

header { display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid #222; padding-bottom: 25px; margin-bottom: 40px; flex-wrap: wrap; gap: 20px; }
.logo-area h1 { color: #ff3333; margin: 0; font-size: 2.2rem; font-weight: 800; letter-spacing: -1.5px; }
.subtitle { margin: 2px 0 0; color: #555; font-size: 0.8rem; text-transform: uppercase; letter-spacing: 3px; font-weight: 600; }

.status-bar { background: #151515; padding: 12px 25px; border-radius: 12px; font-size: 0.85rem; display: flex; align-items: center; gap: 15px; border: 1px solid #222; color: #aaa; }
.dot { width: 10px; height: 10px; background: #ff3333; border-radius: 50%; }
.status-bar.online .dot { background: #00ff00; box-shadow: 0 0 10px rgba(0, 255, 0, 0.4); }
.divider { color: #333; font-weight: 100; }

.crawl-form { background: #111; padding: 30px; border-radius: 16px; margin-bottom: 20px; border: 1px solid #1a1a1a; box-shadow: 0 4px 20px rgba(0,0,0,0.3); }
.crawl-form h2 { margin-top: 0; font-size: 1.1rem; margin-bottom: 20px; color: #777; }
.input-group { display: flex; gap: 12px; }
input { flex: 1; padding: 16px 20px; border-radius: 10px; border: 1px solid #222; background: #050505; color: #fff; font-size: 1rem; }
button { padding: 0 35px; background: #ff3333; border: none; color: white; font-weight: 700; border-radius: 10px; cursor: pointer; }

.search-group { margin-bottom: 30px; position: relative; }
.search-icon { position: absolute; left: 15px; top: 18px; font-size: 1.2rem; }
.search-group input { padding-left: 45px; background: #151515; border-color: #333; }
.search-group input:focus { border-color: #ff3333; }

.view-controls { display: flex; gap: 15px; margin-bottom: 30px; justify-content: center; }
.toggle-btn { background: #222; color: #888; padding: 12px 25px; border-radius: 30px; transition: all 0.3s; cursor: pointer; border: none; font-weight: 600;}
.toggle-btn.active { background: #ff3333; color: white; box-shadow: 0 4px 15px rgba(255, 51, 51, 0.4); }

.section-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 15px; }
.btn-refresh { background: transparent; border: 1px solid #333; color: #aaa; padding: 8px 15px; border-radius: 8px; cursor: pointer;}
.graph-actions { display: flex; gap: 10px; }
.btn-action { background: #222; border: 1px solid #333; color: #fff; padding: 8px 15px; border-radius: 8px; cursor: pointer; font-size: 0.85rem; }
.btn-action.off { background: #111; color: #666; }
.btn-action.primary { background: #ff3333; border: none; }

.graph-section { background: #111; padding: 20px; border-radius: 16px; border: 1px solid #1a1a1a; box-shadow: inset 0 0 50px rgba(0,0,0,0.5); }
.graph-container { width: 100%; height: 750px; background: radial-gradient(circle, #1a1a1a 0%, #050505 100%); border: 1px solid #222; border-radius: 12px; margin-top: 15px; outline: none; }

.legend { display: flex; gap: 20px; padding: 12px; background: #161616; border-radius: 8px; font-size: 0.85rem; justify-content: center; border: 1px solid #222;}
.legend-item { display: flex; align-items: center; gap: 8px; color: #888; font-weight: 600;}
.dot-legend { width: 12px; height: 12px; border-radius: 50%; display: inline-block; }
.l-green { background: #00ff00; }
.l-yellow { background: #ffcc00; }
.l-red { background: #ff3333; }
.l-gray { background: #888888; }

.status-pill { font-size: 0.7rem; padding: 2px 8px; border-radius: 10px; text-transform: uppercase; font-weight: 800; background: #222; color: #888; }
.status-pill.pending_v2 { color: #ffcc00; background: rgba(255, 204, 0, 0.1); }
.status-pill.crawling { color: #4da6ff; background: rgba(77, 166, 255, 0.1); animation: pulse 2s infinite; }
.status-pill.completed { color: #00ff00; background: rgba(0, 255, 0, 0.1); }
.status-pill.failed { color: #ff3333; background: rgba(255, 51, 51, 0.1); }

@keyframes pulse { 0% { opacity: 1; } 50% { opacity: 0.5; } 100% { opacity: 1; } }
.title-row { display: flex; align-items: center; gap: 10px; }
table { width: 100%; border-collapse: collapse; }
th, td { padding: 15px 20px; text-align: left; border-bottom: 1px solid #151515; }
th { background: #0d0d0d; color: #444; font-weight: 700; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 1.5px; }
.url { font-family: 'JetBrains Mono', 'Fira Code', monospace; color: #4da6ff; font-size: 0.85rem; max-width: 250px; overflow: hidden; text-overflow: ellipsis; }

@media (max-width: 768px) {
  .container { padding: 15px; }
  header { flex-direction: column; align-items: flex-start; }
  .input-group { flex-direction: column; }
  .col-server, .col-id { display: none; }
  .legend { flex-wrap: wrap; }
}
</style>