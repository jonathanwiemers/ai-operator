<script setup lang="ts">
import { loginURL } from '../api'

// The server sends error codes, not text, so a link cannot put words on
// this page. Show the error once, and do not come back to it after the login.
const messages: Record<string, string> = {
  unreachable: 'The identity provider cannot be reached. Please try again later.',
  expired: 'The login expired or was started in another browser. Please sign in again.',
  state: 'The login could not be verified. Please sign in again.',
  refused: 'The identity provider refused the login.',
  token: 'The identity provider\'s answer could not be verified.',
  claims: 'Your account\'s user name could not be read from the login. Please contact your administrator.',
  session: 'The session could not be started. Please contact your administrator.',
}
const url = new URL(window.location.href)
const code = url.searchParams.get('login_error')
const error =
  code === null ? null : Object.hasOwn(messages, code) ? messages[code] : 'The login failed. Please sign in again.'
if (code !== null) {
  url.searchParams.delete('login_error')
  window.history.replaceState(null, '', url.pathname + url.search)
}
const href = loginURL()
</script>

<template>
  <div class="signin">
    <div class="box card">
      <img src="/logo.svg" alt="" width="56" height="56" />
      <h1>ai-operator</h1>
      <p class="muted">Coding agents that work through your GitLab board. Sign in to follow their work.</p>
      <div v-if="error" class="banner danger" role="alert">{{ error }}</div>
      <a :href="href" class="btn primary signin-btn">Sign in with SSO</a>
    </div>
  </div>
</template>

<style scoped>
.signin {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
  background:
    radial-gradient(circle at 20% 10%, color-mix(in srgb, var(--accent-2) 14%, transparent), transparent 45%),
    radial-gradient(circle at 80% 90%, color-mix(in srgb, var(--brand-ink) 14%, transparent), transparent 45%),
    var(--bg);
}

.box {
  width: min(400px, 100%);
  padding: 36px 32px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.box img {
  border-radius: 14px;
}

.box p {
  margin: 0;
}

.banner {
  text-align: left;
  width: 100%;
}

.signin-btn {
  margin-top: 8px;
  width: 100%;
  justify-content: center;
  height: 40px;
}
</style>
