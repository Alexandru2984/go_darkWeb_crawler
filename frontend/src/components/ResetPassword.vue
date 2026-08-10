<script setup>
import { ref } from 'vue'
import { validateNewPassword } from '../lib/password.js'
import { csrfHeaders } from '../lib/csrf.js'

const props = defineProps({
  token: { type: String, required: true },
})

const password = ref('')
const confirm = ref('')
const message = ref('')
const isError = ref(false)
const done = ref(false)
const submitting = ref(false)

const submit = async () => {
  isError.value = false
  const err = validateNewPassword(password.value, confirm.value)
  if (err) {
    isError.value = true
    message.value = err
    return
  }
  submitting.value = true
  message.value = ''
  try {
    const res = await fetch('/api/auth/reset', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...csrfHeaders('POST') },
      body: JSON.stringify({ token: props.token, password: password.value }),
    })
    const data = await res.json().catch(() => ({}))
    if (res.ok) {
      done.value = true
      message.value = data.message || 'Password updated. You can now log in.'
    } else {
      isError.value = true
      message.value = data.error || 'Could not reset password. The link may be invalid or expired.'
    }
  } catch {
    isError.value = true
    message.value = 'Connection error. Please try again.'
  } finally {
    submitting.value = false
  }
}

// After success (or to abandon), go back to the app root — drops the token
// from the URL so it isn't left in history.
const goToLogin = () => {
  window.location.assign('/')
}
</script>

<template>
  <div class="reset-wrapper">
    <div class="reset-box">
      <h1>🕷️ Onion Spider</h1>
      <h2>Reset your password</h2>

      <template v-if="!done">
        <div class="input-group">
          <input
            v-model="password"
            type="password"
            placeholder="New password"
            autocomplete="new-password"
            @keyup.enter="submit"
          />
        </div>
        <div class="input-group" style="margin-top: 12px">
          <input
            v-model="confirm"
            type="password"
            placeholder="Confirm new password"
            autocomplete="new-password"
            @keyup.enter="submit"
          />
        </div>
        <button :disabled="submitting" style="margin-top: 18px; width: 100%" @click="submit">
          {{ submitting ? 'Saving…' : 'Set new password' }}
        </button>
      </template>

      <template v-else>
        <button style="margin-top: 18px; width: 100%" @click="goToLogin">Back to login</button>
      </template>

      <p v-if="message" :class="isError ? 'msg error' : 'msg ok'">{{ message }}</p>
      <p class="back-link" @click="goToLogin">← Back to login</p>
    </div>
  </div>
</template>

<style scoped>
.reset-wrapper {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #0a0a0a;
  color: #e0e0e0;
  font-family: 'Inter', system-ui, sans-serif;
}
.reset-box {
  width: 360px;
  max-width: 90vw;
  background: #141414;
  border: 1px solid #2a2a2a;
  border-radius: 12px;
  padding: 32px;
  text-align: center;
}
.reset-box h1 {
  margin: 0 0 4px;
  font-size: 1.5rem;
}
.reset-box h2 {
  margin: 0 0 20px;
  font-size: 1.05rem;
  font-weight: 500;
  color: #9aa0a6;
}
.input-group input {
  width: 100%;
  box-sizing: border-box;
  padding: 12px 14px;
  background: #0e0e0e;
  border: 1px solid #2a2a2a;
  border-radius: 8px;
  color: #e0e0e0;
  font-size: 0.95rem;
}
button {
  padding: 12px 16px;
  background: #4da6ff;
  color: #0a0a0a;
  border: none;
  border-radius: 8px;
  font-weight: 600;
  cursor: pointer;
}
button:disabled {
  opacity: 0.6;
  cursor: default;
}
.msg {
  margin-top: 16px;
  font-size: 0.9rem;
}
.msg.error {
  color: #ff6b6b;
}
.msg.ok {
  color: #51cf66;
}
.back-link {
  margin-top: 18px;
  color: #4da6ff;
  cursor: pointer;
  font-size: 0.85rem;
}
</style>
