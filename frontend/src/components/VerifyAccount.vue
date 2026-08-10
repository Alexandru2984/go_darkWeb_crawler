<script setup>
import { ref } from "vue";
import { csrfHeaders } from "../lib/csrf.js";

const props = defineProps({
  token: { type: String, required: true },
});

const message = ref("");
const isError = ref(false);
const done = ref(false);
const submitting = ref(false);

const submit = async () => {
  submitting.value = true;
  isError.value = false;
  message.value = "";
  try {
    const res = await fetch("/api/auth/verify", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
        ...csrfHeaders("POST"),
      },
      body: JSON.stringify({ token: props.token }),
    });
    const data = await res.json().catch(() => ({}));
    if (res.ok) {
      done.value = true;
      message.value =
        data.message || "Account successfully verified. You can now log in.";
    } else {
      isError.value = true;
      message.value =
        data.error ||
        "Could not verify this account. The link may be invalid or expired.";
    }
  } catch {
    isError.value = true;
    message.value = "Connection error. Please try again.";
  } finally {
    submitting.value = false;
  }
};

const goToLogin = () => window.location.assign("/");
</script>

<template>
  <main class="verify-wrapper">
    <section class="verify-box" aria-labelledby="verify-title">
      <h1>🕷️ Onion Spider</h1>
      <h2 id="verify-title">Confirm account activation</h2>
      <p v-if="!done">
        The link has not activated anything yet. Confirm when you are ready.
      </p>

      <button v-if="!done" type="button" :disabled="submitting" @click="submit">
        {{ submitting ? "Confirming…" : "Confirm account" }}
      </button>
      <button v-else type="button" @click="goToLogin">Back to login</button>

      <p
        v-if="message"
        aria-live="polite"
        :class="['message', { error: isError }]"
      >
        {{ message }}
      </p>
      <button v-if="!done" type="button" class="back-link" @click="goToLogin">
        ← Back to login
      </button>
    </section>
  </main>
</template>

<style scoped>
.verify-wrapper {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 20px;
  background: #0a0a0a;
  color: #e0e0e0;
  font-family: Inter, system-ui, sans-serif;
}
.verify-box {
  width: min(360px, 100%);
  box-sizing: border-box;
  padding: 32px;
  text-align: center;
  background: #141414;
  border: 1px solid #2a2a2a;
  border-radius: 12px;
}
h1 {
  margin: 0 0 4px;
  font-size: 1.5rem;
}
h2 {
  margin: 0 0 16px;
  font-size: 1.05rem;
  color: #b5bac1;
}
p {
  color: #b5bac1;
  line-height: 1.5;
}
button {
  width: 100%;
  min-height: 44px;
  margin-top: 16px;
  padding: 12px 16px;
  cursor: pointer;
  color: #07111a;
  font-weight: 700;
  background: #71b7ff;
  border: 0;
  border-radius: 8px;
}
button:disabled {
  cursor: wait;
  opacity: 0.65;
}
.back-link {
  color: #71b7ff;
  background: transparent;
}
.message {
  color: #51cf66;
}
.message.error {
  color: #ff8787;
}
</style>
