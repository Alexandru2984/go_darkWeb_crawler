<script setup>
import { ref } from "vue";
import { apiJSON } from "../lib/api.js";
import { validateNewPassword } from "../lib/password.js";

const props = defineProps({
  mfaEnabled: Boolean,
  disabled: Boolean,
});
const emit = defineEmits(["changed", "busy"]);
const currentPassword = ref("");
const newPassword = ref("");
const confirmation = ref("");
const code = ref("");
const showPassword = ref(false);
const busy = ref(false);
const error = ref("");

const submit = async () => {
  if (busy.value || props.disabled) return;
  error.value = validateNewPassword(newPassword.value, confirmation.value);
  if (error.value) return;
  if (currentPassword.value === newPassword.value) {
    error.value = "Choose a password different from your current password.";
    return;
  }
  busy.value = true;
  emit("busy", true);
  try {
    const result = await apiJSON("/api/auth/password", {
      method: "POST",
      json: {
        password: currentPassword.value,
        new_password: newPassword.value,
        code: code.value.trim(),
      },
    });
    if (!result.ok) {
      error.value = result.data.error || "Could not update your password.";
      return;
    }
    newPassword.value = "";
    confirmation.value = "";
    emit("changed");
  } finally {
    currentPassword.value = "";
    code.value = "";
    busy.value = false;
    emit("busy", false);
  }
};
</script>

<template>
  <section class="card" aria-labelledby="password-heading">
    <h2 id="password-heading" class="password-title">Change password</h2>
    <p id="password-effect" class="muted password-description">
      Use a unique password you do not use elsewhere. Updating it signs out
      every device, including this one. Your authenticator stays enabled.
    </p>
    <p v-if="error" class="msg msg--error" role="alert">{{ error }}</p>
    <form @submit.prevent="submit">
      <fieldset :disabled="disabled || busy" class="password-fields">
        <div>
          <label class="label" for="change-current-password"
            >Current password</label
          >
          <input
            id="change-current-password"
            v-model="currentPassword"
            class="field"
            type="password"
            autocomplete="current-password"
            required
          />
        </div>
        <div class="password-pair">
          <div>
            <label class="label" for="change-new-password">New password</label>
            <input
              id="change-new-password"
              v-model="newPassword"
              class="field"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="new-password"
              required
            />
          </div>
          <div>
            <label class="label" for="change-confirm-password"
              >Confirm new password</label
            >
            <input
              id="change-confirm-password"
              v-model="confirmation"
              class="field"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="new-password"
              required
            />
          </div>
        </div>
        <label class="password-toggle">
          <input v-model="showPassword" type="checkbox" />
          Show new password
        </label>
        <div v-if="mfaEnabled">
          <label class="label" for="change-password-code"
            >Authenticator or recovery code</label
          >
          <input
            id="change-password-code"
            v-model="code"
            class="field"
            type="text"
            autocomplete="one-time-code"
            maxlength="32"
            autocapitalize="none"
            spellcheck="false"
            required
          />
        </div>
        <div>
          <button class="btn" type="submit" aria-describedby="password-effect">
            {{ busy ? "Updating password…" : "Update password" }}
          </button>
        </div>
      </fieldset>
    </form>
  </section>
</template>

<style scoped>
.password-title {
  font-size: 1.05rem;
  margin-bottom: var(--s-3);
}
.password-description {
  margin-bottom: var(--s-4);
  font-size: 0.92rem;
}
.password-fields {
  border: 0;
  margin: 0;
  padding: 0;
  min-width: 0;
  display: grid;
  gap: var(--s-4);
}
.password-fields:disabled {
  opacity: 0.65;
}
.password-pair {
  display: grid;
  gap: var(--s-4);
}
.password-pair > div {
  min-width: 0;
}
.password-toggle {
  display: flex;
  align-items: center;
  gap: var(--s-2);
  font-size: 0.85rem;
}
@media (min-width: 680px) {
  .password-pair {
    grid-template-columns: 1fr 1fr;
  }
}
</style>
