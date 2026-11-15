<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { nextTick, ref, watch } from 'vue'
import { ApiError, getTaskLogs, LoginRequiredError } from '../client'

// TaskLogs is the task page's Logs tab (run watchdog R8): the last 500
// lines of the task pod's runner or agent container log, scrolled to the
// end, with Reload and Download. Only the task's owners see the tab.
const props = defineProps<{ namespace: string; name: string; task: string }>()
const containers = ['runner', 'agent'] as const
const container = ref<(typeof containers)[number]>('runner')
const text = ref('')
const note = ref('')
const pre = ref<HTMLPreElement>()
let seq = 0

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
    note.value = e instanceof ApiError && e.status === 403 ? 'Only the task’s owners may read its logs.' : e instanceof Error ? e.message : String(e)
  }
}
// A switch clears the other container's log at once.
watch(container, () => (text.value = ''))
watch(() => [props.namespace, props.name, props.task, container.value], load, { immediate: true })

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
    <div class="mb-2.5 flex flex-wrap items-center gap-2 text-[13px]">
      <button
        v-for="c in containers"
        :key="c"
        type="button"
        class="cursor-pointer rounded-(--radius-sm) border px-2.5 py-1 font-[inherit]"
        :class="c === container ? 'border-(--border-strong) bg-(--surface-2) text-(--text)' : 'border-transparent bg-transparent text-(--text-2)'"
        :aria-pressed="c === container"
        @click="container = c"
      >
        {{ c }}
      </button>
      <span class="flex-1" />
      <UButton color="neutral" variant="outline" label="Reload" @click="load" />
      <UButton color="neutral" variant="outline" label="Download" :disabled="!text" @click="download" />
    </div>
    <div v-if="note" class="card empty">{{ note }}</div>
    <pre
      v-if="text"
      ref="pre"
      data-task-logs
      class="card m-0 max-h-[70vh] overflow-y-auto p-3 font-mono text-xs break-words whitespace-pre-wrap"
    >{{ text }}</pre>
  </section>
</template>
