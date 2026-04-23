import re

with open('/home/micu/go/frontend/src/App.vue', 'r') as f:
    content = f.read()

# Inject auth variables
auth_vars = """
const userToken = ref(localStorage.getItem('token') || '')
const userRole = ref(localStorage.getItem('role') || '')
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

const handleAuth = async () => {
  authMessage.value = 'Se proceseaza...'
  const endpoint = authMode.value === 'login' ? '/api/auth/login' : '/api/auth/register'
  
  try {
    const res = await fetch(`${API_BASE.replace('/api', '')}${endpoint}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: authEmail.value, password: authPassword.value })
    })
    const data = await res.json()
    
    if (res.ok) {
      if (authMode.value === 'login') {
        userToken.value = data.token
        userRole.value = data.role
        localStorage.setItem('token', data.token)
        localStorage.setItem('role', data.role)
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
  localStorage.removeItem('token')
  localStorage.removeItem('role')
  status.value = { status: 'offline', nodes_crawled: 0, pending_nodes: 0, db_connected: false, active_workers: 0 }
  nodes.value = []
  edges.value = []
}
"""

content = content.replace("const API_BASE = '/api'", "const API_BASE = '/api'\n" + auth_vars)

# Inject headers into fetches
content = re.sub(r"fetch\(`\$\{API_BASE\}/status`\)", r"fetch(`${API_BASE}/status`, { headers: getAuthHeaders() })", content)
content = re.sub(r"fetch\(`\$\{API_BASE\}/nodes`\)", r"fetch(`${API_BASE}/nodes`, { headers: getAuthHeaders() })", content)
content = re.sub(r"fetch\(`\$\{API_BASE\}/edges`\)", r"fetch(`${API_BASE}/edges`, { headers: getAuthHeaders() })", content)
content = re.sub(r"fetch\(`\$\{API_BASE\}/search\?q=\$\{encodeURIComponent\(searchQuery\.value\)\}`\)", r"fetch(`${API_BASE}/search?q=${encodeURIComponent(searchQuery.value)}`, { headers: getAuthHeaders() })", content)

# Inject header in startCrawl
content = content.replace("headers: { 'Content-Type': 'application/json' },", "headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },")

# Add Login UI to template
login_ui = """
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
"""

content = content.replace("<main>", login_ui)

# Add logout button
header_add = """
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
          <span v-if="isLoggedIn" style="cursor: pointer; color: #ff3333; font-weight: bold;" @click="logout">Logout</span>
        </div>
"""

content = re.sub(r'<div class="status-bar".*?</div>', header_add, content, flags=re.DOTALL)

with open('/home/micu/go/frontend/src/App.vue', 'w') as f:
    f.write(content)
