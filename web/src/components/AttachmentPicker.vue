<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UTooltip from '@nuxt/ui/components/Tooltip.vue'
import { ref, useTemplateRef } from 'vue'
import { accept, checkFiles, fileSize, type Refused } from '../attachments'
import { announce } from '../ui/useAnnounce'

// AttachmentPicker is the files of a message before Send (docs/contributing/web-ui.md,
// "Composer"): the paperclip, which opens the file dialog, and a chip per
// file with its remove button, and the refusals of the browser's checks.
// It has two roots: the chips (with the refusals) take a row of their own
// in a wrapping flex row (basis-full, order-first), and the paperclip stays
// where the caller puts it. Paste and drop come in through add.
defineProps<{ disabled?: boolean }>()
const files = defineModel<File[]>({ required: true })

const refused = ref<Refused[]>([])
const paperclip = useTemplateRef<InstanceType<typeof UButton>>('paperclip')

// add takes the files that pass the checks and says what was attached and
// what was refused.
function add(added: File[]) {
  if (!added.length) return
  const next = checkFiles(files.value, added)
  const taken = next.files.slice(files.value.length)
  files.value = next.files
  refused.value = next.refused
  if (taken.length) announce(`Attached ${taken.map((f) => f.name).join(', ')}`)
  if (next.refused.length) announce(next.refused.map((r) => r.reason).join('. '), { assertive: true })
}

function remove(i: number) {
  const gone = files.value[i]
  files.value = files.value.filter((_, j) => j !== i)
  refused.value = []
  if (gone) announce(`Removed ${gone.name}`)
  // The chip's button is gone: the focus goes to the paperclip.
  ;(paperclip.value?.$el as HTMLElement | undefined)?.focus()
}

// open shows the file dialog of an input made off the page, as Nuxt UI's
// useFileUpload does without an inputRef: there is no file input in the
// markup, and the composable's drop zone code stays out of the initial
// bundle (docs/contributing/web-ui.md, "Composer"). The input is kept while
// the picker lives, and emptied before each open, so the same file can be
// picked again.
let input: HTMLInputElement | undefined
function open() {
  if (!input) {
    const el = (input = document.createElement('input'))
    el.type = 'file'
    el.multiple = true
    el.accept = accept
    el.onchange = () => add([...(el.files ?? [])])
  }
  input.value = ''
  input.click()
}

defineExpose({ add })
</script>

<template>
  <div v-if="files.length || refused.length" class="order-first flex basis-full flex-col gap-1 pb-1" data-attachments>
    <ul v-if="files.length" class="m-0 flex list-none flex-wrap gap-1.5 p-0" aria-label="Attached files">
      <li
        v-for="(f, i) in files"
        :key="`${i}:${f.name}:${f.size}`"
        class="flex max-w-full items-center gap-1.5 rounded-full border border-(--border) bg-(--surface-2) p-0.5 pl-3 text-xs text-(--text)"
        data-attachment-chip
      >
        <span class="max-w-48 truncate">{{ f.name }}</span>
        <span class="whitespace-nowrap text-(--text-3)">{{ fileSize(f.size) }}</span>
        <UButton
          icon="i-lucide-x"
          color="neutral"
          variant="ghost"
          size="xs"
          class="rounded-full"
          :aria-label="`Remove ${f.name}`"
          :disabled="disabled"
          @mousedown.prevent
          @click="remove(i)"
        />
      </li>
    </ul>
    <p v-for="r in refused" :key="r.reason" class="m-0 text-xs text-(--tone-danger-fg)" data-attachment-refused>{{ r.reason }}</p>
  </div>
  <UTooltip text="Attach files">
    <UButton
      ref="paperclip"
      icon="i-lucide-paperclip"
      color="neutral"
      variant="ghost"
      aria-label="Attach files"
      :disabled="disabled"
      @mousedown.prevent
      @click="open()"
    />
  </UTooltip>
</template>
