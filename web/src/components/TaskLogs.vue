<script setup lang="ts">
import { errorMessage } from '../format'
import UButton from '@nuxt/ui/components/Button.vue'
import UDashboardToolbar from '@nuxt/ui/components/DashboardToolbar.vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { ApiError, followTaskLogs, getTaskLogs, LoginRequiredError } from '../client'
import { appendLog } from '../logTail'
import { toolbarUi } from '../pageHeader'

// TaskLogs is the task page's Logs tab (run watchdog R8): the last 500
// lines of the task pod's runner or agent container log, scrolled to the
// end, with Reload, Follow and Download. Only the task's owners see the tab.
const props = defineProps<{ namespace: string; name: string; task: string }>()
const containers = ['runner', 'agent'] as const
const container = ref<(typeof containers)[number]>('runner')
const text = ref('')
const note = ref('')
const pre = ref<HTMLPreElement>()
const follow = ref(false)
let seq = 0
// reading aborts the followed request; set while one runs.
let reading: AbortController | undefined

function errorText(e: unknown): string {
  return e instanceof ApiError && e.status === 403 ? 'Only the task’s owners may read its logs.' : errorMessage(e)
}

function stop() {
  reading?.abort()
  reading = undefined
}

async function load() {
  const mine = ++seq
  note.value = 'Loading…'
  try {
    const t = await getTaskLogs(props.namespace, props.name, props.task, container.value)
    if (mine !== seq) return
    ;[text.value, note.value] = [t, t ? '' : 'The log is empty.']
    await nextTick()
    pre.value?.scrollTo(0, pre.value.scrollHeight)
  } catch (e) {
    if (mine !== seq || e instanceof LoginRequiredError) return
    text.value = ''
    note.value = errorText(e)
  }
}

// startFollow reads the last 500 lines and what comes after, keeps the last
// 2000 and the end in view unless the user scrolled up. When the stream
// ends (the container stopped or the session is due for renewal; the
// client cannot tell which) or fails, follow turns off with a note. The
// shown snapshot stays until the first chunk replaces it.
async function startFollow() {
  ++seq
  stop()
  const ctrl = (reading = new AbortController())
  note.value = 'Following…'
  try {
    const reader = (await followTaskLogs(props.namespace, props.name, props.task, container.value, ctrl.signal)).getReader()
    const dec = new TextDecoder()
    for (let fresh = true; ; fresh = false) {
      const r = await reader.read()
      // A chunk read before an abort belongs to a superseded stream.
      if (reading !== ctrl) return
      if (r.done) break
      const el = pre.value
      const atEnd = fresh || !el || el.scrollHeight - el.scrollTop - el.clientHeight < 8
      ;[text.value, note.value] = [appendLog(fresh ? '' : text.value, dec.decode(r.value, { stream: true })), '']
      if (atEnd) nextTick(() => pre.value?.scrollTo(0, pre.value.scrollHeight))
    }
    note.value = 'Follow ended: the container stopped or the session was renewed. Press Follow to go on.'
  } catch (e) {
    if (ctrl.signal.aborted || e instanceof LoginRequiredError) return
    note.value = errorText(e)
  }
  if (reading === ctrl) [reading, follow.value] = [undefined, false]
}

// A switch clears the other container's log at once.
watch(container, () => (text.value = ''))
watch(() => [props.namespace, props.name, props.task, container.value], () => (follow.value ? startFollow() : load()), { immediate: true })
// Turning follow off keeps the shown text, or loads the snapshot when no
// line came yet. A stream that ended turned it off itself (reading unset).
// Leaving the tab unmounts.
watch(follow, (on) => {
  if (on) return void startFollow()
  if (!reading) return
  stop()
  if (text.value) note.value = ''
  else void load()
})
onBeforeUnmount(stop)

// download saves the shown text through a Blob URL; no request is made.
// The anchor is in the document while clicked: Safari ignores a click on a
// detached one (as downloadCsv in costs.ts).
function download() {
  const a = Object.assign(document.createElement('a'), {
    href: URL.createObjectURL(new Blob([text.value], { type: 'text/plain;charset=utf-8' })),
    download: `${props.task}-${container.value}.log`,
  })
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(a.href), 0)
}
</script>

<template>
  <section class="section">
    <!-- The tab's toolbar (decision 3): the container pills left, the log
         actions right. -->
    <UDashboardToolbar class="text-[13px]" :ui="toolbarUi" data-tab-toolbar>
      <template #left>
        <UTabs
          v-model="container"
          :items="containers.map((c) => ({ label: c, value: c }))"
          variant="pill"
          size="sm"
          :content="false"
          class="min-w-0 max-w-full"
          :ui="{ list: 'w-auto max-w-full overflow-x-auto' }"
          aria-label="Container"
        />
      </template>
      <template #right>
        <UButton color="neutral" variant="outline" label="Reload" :disabled="follow" @click="load" />
        <UButton color="neutral" :variant="follow ? 'solid' : 'outline'" label="Follow" :aria-pressed="follow" @click="follow = !follow" />
        <UButton color="neutral" variant="outline" label="Download" :disabled="!text" @click="download" />
      </template>
    </UDashboardToolbar>
    <div v-if="note" class="card empty">{{ note }}</div>
    <pre
      v-if="text"
      ref="pre"
      data-task-logs
      class="card m-0 max-h-[70vh] overflow-y-auto p-3 font-mono text-xs break-words whitespace-pre-wrap"
    >{{ text }}</pre>
  </section>
</template>
