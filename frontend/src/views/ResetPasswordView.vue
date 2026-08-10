<script setup>
import { ref, onMounted } from "vue";
import { useRouter } from "vue-router";
import { apiJSON } from "../lib/api.js";
import { takeLinkToken } from "../lib/linkTokens.js";
import { validateNewPassword } from "../lib/password.js";
import AuthShell from "../components/AuthShell.vue";

const router = useRouter();
const token = ref(takeLinkToken());

const password = ref("");
const confirmValue = ref("");
const message = ref("");
const kind = ref("info");
const done = ref(false);
const busy = ref(false);

onMounted(() => {
  if (!token.value) {
    message.value =
      "This reset link is missing or malformed. Request a new one from the sign-in page.";
    kind.value = "error";
  }
});

const submit = async () => {
  const problem = validateNewPassword(password.value, confirmValue.value);
  if (problem) {
    message.value = problem;
    kind.value = "error";
    return;
  }
  busy.value = true;
  const { ok, data } = await apiJSON("/api/auth/reset", {
    method: "POST",
    json: { token: token.value, password: password.value },
  });
  busy.value = false;
  password.value = "";
  confirmValue.value = "";
  if (ok) {
    done.value = true;
    token.value = "";
    message.value =
      data.message ||
      "Password updated. Every existing session was signed out.";
    kind.value = "ok";
  } else {
    message.value =
      data.error ||
      "Could not reset the password. The link may be expired or already used.";
    kind.value = "error";
  }
};
</script>

<template>
  <AuthShell title="Choose a new password">
    <form
      v-if="!done && token"
      class="stack"
      novalidate
      @submit.prevent="submit"
    >
      <div>
        <label class="label" for="new-password">New password</label>
        <input
          id="new-password"
          v-model="password"
          class="field"
          type="password"
          autocomplete="new-password"
          required
        />
      </div>
      <div>
        <label class="label" for="confirm-password">Confirm new password</label>
        <input
          id="confirm-password"
          v-model="confirmValue"
          class="field"
          type="password"
          autocomplete="new-password"
          required
        />
      </div>
      <button class="btn btn--block" type="submit" :disabled="busy">
        {{ busy ? "Saving…" : "Set new password" }}
      </button>
    </form>

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
.back {
  margin-top: var(--s-4);
}
</style>
