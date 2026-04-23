<script setup>
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { Network } from 'vis-network'

const status = ref({ status: 'offline', nodes_crawled: 0, pending_nodes: 0, db_connected: false, active_workers: 0 })
const nodes = ref([])
const edges = ref([])
const targetUrl = ref('')
const searchQuery = ref('')
const selectedCategory = ref('all')
const loading = ref(false)
const isSearching = ref(false)
const isGraphView = ref(false)
const physicsEnabled = ref(true)
const message = ref('')
const messageType = ref('info') // 'info' | 'error'
const toast = ref('')
let toastTimer = null

const graphContainer = ref(null)
let network = null
let statusInterval = null

const API_BASE = '/api'

const userToken = ref(localStorage.getItem('token') || '')
const userRole = ref(localStorage.getItem('role') || '')
const userEmail = ref(localStorage.getItem('email') || '')
const authEmail = ref('')
const authPassword = ref('')
const authMode = ref('login') // 'login' sau 'register'
const isLoggedIn = computed(() => !!userToken.value)

const authMessage = ref('')

const getAuthHeaders = () => {
  const headers = {}
  if (userToken.value) {
    headers['Authorization'] = `Bearer ${userToken.value}`
  }
  return headers
}

// apiFetch e un wrapper peste fetch care auto-logout daca backend-ul returneaza 401
// (token expirat sau invalidat prin rotatie JWT_SECRET). Previne stari inconsistente
// unde UI-ul crede ca esti logat dar orice apel esueaza tacit.
const apiFetch = async (url, opts = {}) => {
  const res = await fetch(url, {
    ...opts,
    headers: { ...(opts.headers || {}), ...getAuthHeaders() }
  })
  if (res.status === 401 && userToken.value) {
    logout()
    authMessage.value = 'Sesiunea a expirat. Intra din nou in cont.'
  }
  return res
}

const handleAuth = async () => {
  authMessage.value = 'Se proceseaza...'
  const endpoint = authMode.value === 'login' ? '/api/auth/login' : '/api/auth/register'
  
  try {
    const res = await fetch(`${API_BASE.replace('/api', '')}${endpoint}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
      body: JSON.stringify({ email: authEmail.value, password: authPassword.value })
    })
    const data = await res.json()
    
    if (res.ok) {
      if (authMode.value === 'login') {
        userToken.value = data.token
        userRole.value = data.role
        userEmail.value = data.email
        localStorage.setItem('token', data.token)
        localStorage.setItem('role', data.role)
        localStorage.setItem('email', data.email)
        authMessage.value = 'Login reusit!'
        setTimeout(() => { authMessage.value = '' }, 2000)
        fetchStatus()
        fetchNodes()
      } else {
        authMessage.value = data.message || 'Cont creat. Verifica email-ul!'
      }
    } else {
      authMessage.value = data.error || 'Eroare la autentificare.'
    }
  } catch (err) {
    authMessage.value = 'Eroare de conexiune.'
  }
}

const logout = () => {
  userToken.value = ''
  userRole.value = ''
  userEmail.value = ''
  localStorage.removeItem('token')
  localStorage.removeItem('role')
  localStorage.removeItem('email')
  status.value = { status: 'offline', nodes_crawled: 0, pending_nodes: 0, db_connected: false, active_workers: 0 }
  nodes.value = []
  edges.value = []
}

const downloadExport = (format) => {
  if (!userToken.value) return

  apiFetch(`${API_BASE}/export?format=${format}`)
  .then(res => {
    if (!res.ok) throw new Error('Eroare export')
    return res.blob()
  })
  .then(blob => {
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.style.display = 'none'
    a.href = url
    
    // Set appropriate extension
    let ext = format
    if (format === 'graphml') ext = 'graphml'
    if (format === 'ndjson') ext = 'ndjson'
    
    a.download = `onion_spider_export.${ext}`
    document.body.appendChild(a)
    a.click()
    window.URL.revokeObjectURL(url)
  })
  .catch(err => {
    console.error(err)
    showToast('Eroare la generarea exportului.')
  })
}


// Culori per categorie — folosite atat in tabel cat si in graf
const CATEGORY_COLORS = {
  marketplace:    '#e74c3c',
  forum:          '#e67e22',
  'search-engine':'#3498db',
  blog:           '#9b59b6',
  wiki:           '#1abc9c',
  directory:      '#f39c12',
  news:           '#27ae60',
  social:         '#e91e63',
  unknown:        '#555555',
}

const CATEGORY_LABELS = {
  marketplace:    '🛒 Marketplace',
  forum:          '💬 Forum',
  'search-engine':'🔍 Motor Căutare',
  blog:           '📝 Blog',
  wiki:           '📚 Wiki',
  directory:      '📁 Director',
  news:           '📰 Știri',
  social:         '👥 Social',
  unknown:        '❓ Necunoscut',
}

const allCategories = Object.keys(CATEGORY_LABELS)

const filteredNodes = computed(() => {
  if (selectedCategory.value === 'all') return nodes.value
  return nodes.value.filter(n => n.category === selectedCategory.value)
})

const showToast = (text) => {
  toast.value = text
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = '' }, 3000)
}

const fetchStatus = async () => {
  try {
    const res = await apiFetch(`${API_BASE}/status`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    status.value = await res.json()
  } catch (err) {
    console.error('Eroare la preluarea statusului:', err)
    status.value = { ...status.value, status: 'offline', db_connected: false }
  }
}

const fetchNodes = async () => {
  if (isSearching.value) return
  if (!userToken.value) return
  try {
    const res = await apiFetch(`${API_BASE}/nodes`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    nodes.value = await res.json() || []
  } catch (err) {
    console.error('Eroare la preluarea nodurilor:', err)
    message.value = 'Eroare la preluarea listei de noduri.'
    messageType.value = 'error'
  }
}

const fetchEdges = async () => {
  if (!userToken.value) return
  try {
    const res = await apiFetch(`${API_BASE}/edges`)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
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
    const res = await apiFetch(`${API_BASE}/search?q=${encodeURIComponent(searchQuery.value)}`)
    if (res.ok) {
      nodes.value = await res.json() || []
    } else {
      message.value = 'Eroare la cautare.'
      messageType.value = 'error'
    }
  } catch (err) {
    console.error('Eroare la cautare:', err)
    message.value = 'Eroare de conexiune la cautare.'
    messageType.value = 'error'
  }
}

const startCrawl = async () => {
  if (!targetUrl.value) return
  loading.value = true
  message.value = 'Se adauga in coada...'
  messageType.value = 'info'
  
  try {
    const res = await apiFetch(`${API_BASE}/crawl`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: targetUrl.value })
    })
    
    if (res.ok) {
      message.value = 'URL adaugat cu succes in coada de scanare!'
      messageType.value = 'info'
      targetUrl.value = ''
      setTimeout(() => { message.value = '' }, 3000)
    } else {
      const text = await res.text()
      message.value = `Eroare: ${text || res.statusText}`
      messageType.value = 'error'
    }
  } catch (err) {
    message.value = 'Eroare de conexiune la API.'
    messageType.value = 'error'
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

  const nodeDegrees = {}
  edges.value.forEach(e => {
    nodeDegrees[e.source] = (nodeDegrees[e.source] || 0) + 1
    nodeDegrees[e.target] = (nodeDegrees[e.target] || 0) + 1
  })

  const visNodes = nodes.value.map(n => {
    // Culoarea principala = categorie, cu fallback pe status HTTP
    const catColor = CATEGORY_COLORS[n.category] || CATEGORY_COLORS.unknown
    let borderColor = catColor
    if (n.status_code === 0 && n.processing_status === 'completed') borderColor = '#ff3333'
    else if (n.processing_status === 'pending') borderColor = '#444'

    const degree = nodeDegrees[n.url] || 0
    const size = Math.min(15 + (degree * 2.5), 60)

    const cleanTitle = n.title ? n.title : 'Sursa Inaccesibila / Secundara'
    const shortLabel = cleanTitle.length > 25 ? cleanTitle.substring(0, 25) + '...' : cleanTitle
    const catLabel = CATEGORY_LABELS[n.category] || n.category
    const tooltipText = `${cleanTitle}\nURL: ${n.url}\nCategorie: ${catLabel}\nStatus HTTP: ${n.status_code} | Legaturi: ${degree}\n(Dublu click pentru a copia link-ul)`

    return {
      id: n.url,
      label: shortLabel,
      title: tooltipText,
      color: {
        background: catColor,
        border: borderColor,
        highlight: { background: '#ffffff', border: catColor },
        hover: { background: catColor, border: '#ffffff' }
      },
      shape: 'dot',
      size,
      borderWidth: size > 30 ? 3 : 1,
      font: { color: '#ccc', size: size > 30 ? 16 : 10, face: 'Inter' },
      shadow: { enabled: true, color: 'rgba(0,0,0,0.6)', size: 10, x: 0, y: 0 }
    }
  })

  const uniqueNodes = Array.from(new Map(visNodes.map(item => [item.id, item])).values())

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
      hideEdgesOnDrag: true
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

  network.on("doubleClick", function (params) {
    if (params.nodes.length > 0) {
      const clickedUrl = params.nodes[0]
      navigator.clipboard.writeText(clickedUrl).then(() => {
        showToast('✅ URL copiat în clipboard!')
      }).catch(() => {
        showToast('❌ Nu am putut copia URL-ul.')
      })
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
  statusInterval = setInterval(() => {
    fetchStatus()
    if (!isGraphView.value) fetchNodes()
  }, 5000)
})

onUnmounted(() => {
  clearInterval(statusInterval)
  if (toastTimer) clearTimeout(toastTimer)
  if (network) network.destroy()
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
          
          <span v-if="isLoggedIn" class="divider">|</span>
          <span v-if="isLoggedIn" style="color: #4da6ff; font-weight: bold;">👤 {{ userEmail }}</span>

          <span v-if="isLoggedIn" class="divider">|</span>
          <span v-if="isLoggedIn" style="cursor: pointer; color: #ff3333; font-weight: bold;" @click="logout">Logout</span>
        </div>

      </header>

      
      <div v-if="!isLoggedIn" class="auth-container">
        <div class="auth-box">
          <h2>{{ authMode === 'login' ? 'Login' : 'Inregistrare' }}</h2>
          <div class="input-group">
            <input v-model="authEmail" type="email" placeholder="Email" @keyup.enter="handleAuth" />
          </div>
          <div class="input-group" style="margin-top: 15px;">
            <input v-model="authPassword" type="password" placeholder="Parola" @keyup.enter="handleAuth" />
          </div>
          <button @click="handleAuth" style="margin-top: 20px; width: 100%;">{{ authMode === 'login' ? 'Intra in cont' : 'Creeaza cont' }}</button>
          <p class="auth-toggle" @click="authMode = authMode === 'login' ? 'register' : 'login'">
            {{ authMode === 'login' ? 'Nu ai cont? Inregistreaza-te' : 'Ai deja cont? Intra' }}
          </p>
          <p v-if="authMessage" class="info-message">{{ authMessage }}</p>
        </div>
      </div>

      <main v-else>

        <section class="crawl-form">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px;">
            <h2 style="margin: 0;">Adauga URL la coada de scanare</h2>
            <div v-if="userRole === 'admin'" style="display: flex; gap: 8px;">
              <span style="color: #888; font-size: 0.8rem; align-self: center; margin-right: 5px;">EXPORT ADMIN:</span>
              <button @click="downloadExport('csv')" style="padding: 5px 15px; font-size: 0.8rem; background: #27ae60;">CSV</button>
              <button @click="downloadExport('xlsx')" style="padding: 5px 15px; font-size: 0.8rem; background: #2980b9;">XLSX</button>
              <button @click="downloadExport('pdf')" style="padding: 5px 15px; font-size: 0.8rem; background: #e74c3c;">PDF</button>
              <button @click="downloadExport('graphml')" style="padding: 5px 15px; font-size: 0.8rem; background: #8e44ad;">GraphML</button>
            </div>
          </div>
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
          <p v-if="message" class="info-message" :class="{ 'error-message': messageType === 'error' }">{{ message }}</p>
        </section>

        <div class="view-controls">
          <button class="toggle-btn" :class="{ active: !isGraphView }" @click="isGraphView = false">📄 Listă / Căutare</button>
          <button class="toggle-btn" :class="{ active: isGraphView }" @click="isGraphView = true">🕸️ Grafic Rețea</button>
        </div>

        <!-- Toast notification pentru clipboard -->
        <transition name="toast-fade">
          <div v-if="toast" class="toast">{{ toast }}</div>
        </transition>

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
            <div class="category-filter">
              <button
                class="cat-btn"
                :class="{ active: selectedCategory === 'all' }"
                @click="selectedCategory = 'all'"
              >Toate</button>
              <button
                v-for="cat in allCategories"
                :key="cat"
                class="cat-btn"
                :class="{ active: selectedCategory === cat }"
                :style="selectedCategory === cat ? { background: CATEGORY_COLORS[cat], borderColor: CATEGORY_COLORS[cat] } : { borderColor: CATEGORY_COLORS[cat] }"
                @click="selectedCategory = cat"
              >{{ CATEGORY_LABELS[cat] }}</button>
            </div>
          </section>

          <section class="nodes-list">
            <div class="section-header">
              <h2>{{ isSearching ? 'Rezultatele Cautarii' : 'Ultimele Site-uri Descoperite' }}
                <span v-if="selectedCategory !== 'all'" class="filter-tag">• {{ CATEGORY_LABELS[selectedCategory] }}</span>
              </h2>
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
                    <th class="col-cat">Categorie</th>
                    <th class="col-server">Server</th>
                    <th class="col-status">Cod</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="node in filteredNodes" :key="node.id">
                    <td class="col-id">{{ node.id }}</td>
                    <td class="url">{{ node.url }}</td>
                    <td>
                      <div class="title-row">
                        <span class="title">{{ node.title || 'In asteptare...' }}</span>
                        <span class="status-pill" :class="node.processing_status.replace('_', '-')">
                          {{ node.processing_status }}
                        </span>
                      </div>
                    </td>
                    <td class="col-cat">
                      <span
                        class="cat-badge"
                        :style="{ background: (CATEGORY_COLORS[node.category] || CATEGORY_COLORS.unknown) + '22', color: CATEGORY_COLORS[node.category] || CATEGORY_COLORS.unknown, borderColor: CATEGORY_COLORS[node.category] || CATEGORY_COLORS.unknown }"
                      >{{ CATEGORY_LABELS[node.category] || node.category }}</span>
                    </td>
                    <td class="col-server">{{ node.server_header || '-' }}</td>
                    <td class="col-status">
                      <span class="badge" :class="{ 's-200': node.status_code === 200 }">
                        {{ node.status_code || '-' }}
                      </span>
                    </td>
                  </tr>
                  <tr v-if="filteredNodes.length === 0">
                    <td colspan="6" class="empty-state">Nu există date încă. Introdu un URL pentru a începe.</td>
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
            <span v-for="cat in allCategories" :key="cat" class="legend-item">
              <span class="dot-legend" :style="{ background: CATEGORY_COLORS[cat] }"></span>
              {{ CATEGORY_LABELS[cat] }}
            </span>
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

.status-pill.pending { color: #888; background: #222; }
.status-pill.pending-v2 { color: #ffcc00; background: rgba(255, 204, 0, 0.1); }
.status-pill.crawling { color: #4da6ff; background: rgba(77, 166, 255, 0.1); animation: pulse 2s infinite; }
.status-pill.completed { color: #00ff00; background: rgba(0, 255, 0, 0.1); }
.status-pill.failed { color: #ff3333; background: rgba(255, 51, 51, 0.1); }

.info-message { color: #00ff00; font-size: 0.9rem; margin-top: 10px; }
.error-message { color: #ff3333 !important; }

.toast {
  position: fixed;
  bottom: 30px;
  left: 50%;
  transform: translateX(-50%);
  background: rgba(20, 20, 20, 0.95);
  color: #fff;
  border: 1px solid #ff3333;
  padding: 12px 28px;
  border-radius: 10px;
  font-size: 0.95rem;
  z-index: 9999;
  box-shadow: 0 4px 20px rgba(0,0,0,0.8);
  pointer-events: none;
}
.toast-fade-enter-active, .toast-fade-leave-active { transition: opacity 0.3s, transform 0.3s; }
.toast-fade-enter-from, .toast-fade-leave-to { opacity: 0; transform: translateX(-50%) translateY(10px); }

@keyframes pulse { 0% { opacity: 1; } 50% { opacity: 0.5; } 100% { opacity: 1; } }
.title-row { display: flex; align-items: center; gap: 10px; }
table { width: 100%; border-collapse: collapse; }
th, td { padding: 15px 20px; text-align: left; border-bottom: 1px solid #151515; }
th { background: #0d0d0d; color: #444; font-weight: 700; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 1.5px; }
.url { font-family: 'JetBrains Mono', 'Fira Code', monospace; color: #4da6ff; font-size: 0.85rem; max-width: 250px; overflow: hidden; text-overflow: ellipsis; }

/* Filtru categorie */
.category-filter { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
.cat-btn { background: transparent; border: 1px solid #333; color: #888; padding: 6px 14px; border-radius: 20px; cursor: pointer; font-size: 0.78rem; font-weight: 600; transition: all 0.2s; }
.cat-btn:hover { color: #fff; }
.cat-btn.active { color: #fff; }

/* Badge categorie in tabel */
.cat-badge { display: inline-block; padding: 3px 10px; border-radius: 12px; border: 1px solid; font-size: 0.75rem; font-weight: 600; white-space: nowrap; }
.col-cat { min-width: 130px; }

/* Tag filtru activ in header sectiune */
.filter-tag { font-size: 0.8rem; color: #888; font-weight: 400; margin-left: 8px; }

@media (max-width: 768px) {
  .container { padding: 15px; }
  header { flex-direction: column; align-items: flex-start; }
  .input-group { flex-direction: column; }
  .col-server, .col-id, .col-cat { display: none; }
  .legend { flex-wrap: wrap; }
}

.auth-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 50vh;
}
.auth-box {
  background: #111;
  padding: 40px;
  border-radius: 16px;
  border: 1px solid #222;
  box-shadow: 0 4px 30px rgba(0,0,0,0.5);
  width: 100%;
  max-width: 400px;
  text-align: center;
}
.auth-box h2 {
  color: #ff3333;
  margin-top: 0;
  margin-bottom: 30px;
}
.auth-toggle {
  color: #888;
  margin-top: 20px;
  cursor: pointer;
  font-size: 0.9rem;
}
.auth-toggle:hover {
  color: #fff;
  text-decoration: underline;
}
</style>