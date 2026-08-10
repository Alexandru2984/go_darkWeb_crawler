<script setup>
import { ref, onMounted } from "vue";
import { useRouter } from "vue-router";
import { apiJSON } from "../lib/api.js";
import { takeLinkToken } from "../lib/linkTokens.js";
import AuthShell from "../components/AuthShell.vue";

const router = useRouter();
// Taken from the fragment at startup and held in memory only; main.js already
// rewrote the address bar so it never reached history.
const token = ref(takeLinkToken());

const message = ref("");
const kind = ref("info");
const done = ref(false);
const busy = ref(false);

onMounted(() => {
  if (!token.value) {
    message.value =
      "This verification link is missing or malformed. Request a new one by signing in.";
    kind.value = "error";
  }
});

// Deliberately a button rather than an automatic call on mount: link-preview
// bots in mail clients fetch URLs, and an account must not be activated by a
// scanner acting in the user's absence.
const confirm = async () => {
  busy.value = true;
  const { ok, data } = await apiJSON("/api/auth/verify", {
    method: "POST",
    headers: { Accept: "application/json" },
    json: { token: token.value },
  });
  busy.value = false;
  if (ok) {
    done.value = true;
    token.value = "";
    message.value = data.message || "Account verified. You can sign in now.";
    kind.value = "ok";
  } else {
    message.value =
      data.error ||
      "Could not verify this account. The link may be expired or already used.";
    kind.value = "error";
  }
};
</script>

<template>
  <AuthShell title="Confirm your account">
    <p v-if="!done && token" class="muted body">
      Nothing has been activated yet. Confirm below when you are ready.
    </p>

    <button
      v-if="!done && token"
      class="btn btn--block"
      type="button"
      :disabled="busy"
      @click="confirm"
    >
      {{ busy ? "Confirming…" : "Confirm account" }}
    </button>

    <p v-if="message" class="msg" :class="`msg--${kind}`" role="alert">
      {{ message }}
    </p>

    <button
      class="btn btn--ghost btn--block back"
      type="button"
      @click="router.push({ name: 'login' })"
    >
      Back to sign in
    </button>
  </AuthShell>
</template>

<style scoped>
.body {
  margin: 0 0 var(--s-4);
}
.back {
  margin-top: var(--s-4);
}
</style>
