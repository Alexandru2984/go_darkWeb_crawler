<script setup>
import { ref, nextTick, useTemplateRef } from "vue";
import { useRoute, useRouter } from "vue-router";
import { apiJSON } from "../lib/api.js";
import { setSession } from "../stores/session.js";
import { passwordStrengthError } from "../lib/password.js";

const route = useRoute();
const router = useRouter();

const mode = ref("login"); // 'login' | 'register'
const email = ref("");
const password = ref("");
const code = ref("");

// The server answers a correct password with totp_required when the account has
// a second factor. Only then does the code field appear — asking everyone for a
// code up front would tell an attacker which accounts have MFA.
const needsCode = ref(false);
const codeField = useTemplateRef("codeField");

const message = ref("");
const messageKind = ref("info");
const busy = ref(false);

const say = (text, kind = "info") => {
  message.value = text;
  messageKind.value = kind;
};

const submit = async () => {
  if (busy.value) return;
  if (!email.value || !password.value) {
    say("Enter your email and password.", "error");
    return;
  }
  if (mode.value === "register") {
    const weak = passwordStrengthError(password.value);
    if (weak) return say(weak, "error");
  }

  busy.value = true;
  say(mode.value === "login" ? "Signing in…" : "Creating account…");

  const endpoint =
    mode.value === "login" ? "/api/auth/login" : "/api/auth/register";
  const payload = { email: email.value, password: password.value };
  if (mode.value === "login" && code.value) payload.code = code.value.trim();

  try {
    const { ok, data } = await apiJSON(endpoint, {
      method: "POST",
      json: payload,
    });

    if (ok && mode.value === "login") {
      setSession({ email: data.email, role: data.role });
      password.value = "";
      code.value = "";
      router.push(route.query.next || "/");
      return;
    }
    if (ok) {
      say(
        data.message || "Account created. Check your email to verify it.",
        "ok",
      );
      mode.value = "login";
      return;
    }

    if (data.totp_required) {
      const first = !needsCode.value;
      needsCode.value = true;
      say(
        first
          ? "Enter the six-digit code from your authenticator app."
          : data.error || "That code was not accepted.",
        first ? "info" : "error",
      );
      await nextTick();
      codeField.value?.focus();
      return;
    }
    say(data.error || "Could not sign in.", "error");
  } catch {
    say("Connection error. Please try again.", "error");
  } finally {
    busy.value = false;
  }
};

const forgot = async () => {
  if (!email.value) {
    return say(
      "Enter your email address first, then choose “Forgot password”.",
      "error",
    );
  }
  busy.value = true;
  try {
    const { data } = await apiJSON("/api/auth/forgot", {
      method: "POST",
      json: { email: email.value },
    });
    // The endpoint answers identically whether or not the account exists.
    say(
      data.message ||
        "If an account exists for that address, a reset link is on its way.",
      "ok",
    );
  } catch {
    say("Connection error. Please try again.", "error");
  } finally {
    busy.value = false;
  }
};

const switchMode = () => {
  mode.value = mode.value === "login" ? "register" : "login";
  needsCode.value = false;
  code.value = "";
  message.value = "";
};
</script>

<template>
  <div class="auth">
    <div class="auth__brand">
      <span class="auth__mark" aria-hidden="true">🕷️</span>
      <h1 class="auth__title">Onion Spider</h1>
      <p class="auth__sub">Recursive deep web explorer</p>
    </div>

    <form class="card auth__card" novalidate @submit.prevent="submit">
      <h2 class="auth__heading">
        {{ mode === "login" ? "Sign in" : "Create an account" }}
      </h2>

      <div class="stack">
        <div>
          <label class="label" for="email">Email</label>
          <input
            id="email"
            v-model="email"
            class="field"
            type="email"
            name="email"
            autocomplete="username"
            required
          />
        </div>

        <div>
          <label class="label" for="password">Password</label>
          <input
            id="password"
            v-model="password"
            class="field"
            type="password"
            name="password"
            :autocomplete="
              mode === 'login' ? 'current-password' : 'new-password'
            "
            required
          />
        </div>

        <div v-if="needsCode">
          <label class="label" for="code">Authenticator code</label>
          <input
            id="code"
            ref="codeField"
            v-model="code"
            class="field field--code"
            type="text"
            name="one-time-code"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="14"
            placeholder="123456"
            aria-describedby="code-hint"
          />
          <p id="code-hint" class="hint">
            Six digits from your authenticator, or one of your recovery codes.
          </p>
        </div>

        <button class="btn btn--block" type="submit" :disabled="busy">
          {{ mode === "login" ? "Sign in" : "Create account" }}
        </button>
      </div>

      <p v-if="message" class="msg" :class="`msg--${messageKind}`" role="alert">
        {{ message }}
      </p>

      <div class="auth__links">
        <button class="linkish" type="button" @click="switchMode">
          {{
            mode === "login"
              ? "Need an account? Register"
              : "Already registered? Sign in"
          }}
        </button>
        <button
          v-if="mode === 'login'"
          class="linkish"
          type="button"
          @click="forgot"
        >
          Forgot password?
        </button>
      </div>
    </form>
  </div>
</template>

<style scoped>
.auth {
  width: 100%;
  max-width: 420px;
}

.auth__brand {
  text-align: center;
  margin-bottom: var(--s-5);
}
.auth__mark {
  font-size: 2.5rem;
  display: block;
}
.auth__title {
  color: var(--accent);
  font-size: 1.8rem;
  margin-top: var(--s-2);
}
.auth__sub {
  color: var(--text-faint);
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.18em;
  font-weight: 600;
  margin: var(--s-1) 0 0;
}

.auth__card {
  padding: var(--s-5) var(--s-4);
}
.auth__heading {
  font-size: 1.15rem;
  margin-bottom: var(--s-4);
}

.field--code {
  font-family: var(--mono);
  letter-spacing: 0.3em;
  text-align: center;
}

.hint {
  margin: var(--s-2) 0 0;
  font-size: 0.8rem;
  color: var(--text-faint);
}

.auth__links {
  display: flex;
  flex-direction: column;
  gap: var(--s-1);
  margin-top: var(--s-4);
  padding-top: var(--s-4);
  border-top: 1px solid var(--border);
}

/* A real <button> so it is keyboard reachable and announced as an action,
   styled to read as a link. */
.linkish {
  background: none;
  border: 0;
  padding: var(--s-2) 0;
  min-height: var(--tap);
  color: var(--text-dim);
  font: inherit;
  font-size: 0.88rem;
  text-align: left;
  cursor: pointer;
}
.linkish:hover {
  color: var(--text);
  text-decoration: underline;
}

@media (min-width: 480px) {
  .auth__card {
    padding: var(--s-6);
  }
}
</style>
