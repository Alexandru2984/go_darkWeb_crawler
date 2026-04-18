<template>
  <div style="font-family: sans-serif; padding: 2rem; max-width: 800px; margin: auto;">
    <h1>🕸️ Onion Spider Dashboard</h1>
    
    <div style="padding: 1rem; border: 1px solid #ccc; border-radius: 8px; margin-bottom: 2rem;">
      <h3>Status Sistem</h3>
      <p>Conexiune API: 
        <strong :style="{color: apiStatus ? 'green' : 'red'}">
          {{ apiStatus ? 'Online' : 'Offline' }}
        </strong>
      </p>
      <div v-if="stats">
        <p>Workeri Activi: <strong>{{ stats.active_workers }}</strong></p>
        <p>Noduri Scrutate: <strong>{{ stats.nodes_crawled }}</strong></p>
        <p>Baza de Date: 
          <strong :style="{color: stats.db_connected ? 'green' : 'orange'}">
            {{ stats.db_connected ? 'Conectat' : 'Asteapta Configurare' }}
          </strong>
        </p>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'

const apiStatus = ref(false)
const stats = ref(null)
let interval = null

const fetchStatus = async () => {
  try {
    // Facem request catre Nginx care va face proxy la Go
    const apiUrl = `/api/status`
    const res = await fetch(apiUrl)
    
    if (res.ok) {
      stats.value = await res.json()
      apiStatus.value = true
    } else {
      apiStatus.value = false
    }
  } catch (error) {
    apiStatus.value = false
  }
}

onMounted(() => {
  fetchStatus()
  // Verificam statusul o data la 3 secunde (polling)
  interval = setInterval(fetchStatus, 3000)
})

onUnmounted(() => {
  clearInterval(interval)
})
</script>
