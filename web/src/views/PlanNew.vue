<script setup lang="ts">
import { errorMessage } from '../format'
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UButton from '@nuxt/ui/components/Button.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UFormField from '@nuxt/ui/components/FormField.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import USelectMenu from '@nuxt/ui/components/SelectMenu.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, createPlan, getPlans } from '../client'
import { useResource } from '../live'
import { pageHeaderUi } from '../pageHeader'
import { planStartGroups, planStartMenuItems } from '../planStart'

// The New plan page (spec plan-anywhere, Decisions 4 and 5): pick a project
// you may steer, write the first message, and Start plan creates the plan
// with it and opens the plan page. ?project=<namespace>/<name> preselects a
// project the picker offers.

// The server's limit of a chat message (internal/ui/chat.go); it checks it again.
const maxMessage = 16384
// The fields the server may name in a 400; other errors show by the button.
const fields = ['project', 'message', 'title'] as const
type Field = (typeof fields)[number]

const route = useRoute()
const router = useRouter()
const { data: overview, error: loadError } = useResource(() => () => getPlans(), ['project'])

const groups = computed(() => planStartGroups(overview.value?.projects ?? []))
const items = computed(() => planStartMenuItems(groups.value))
const offered = computed(() => groups.value.flatMap((g) => g.items))

const project = ref<string | undefined>()
const message = ref('')
const title = ref('')
const errors = reactive<Partial<Record<Field, string>>>({})
const formError = ref<string | null>(null)
const sending = ref(false)

const selected = computed(() => offered.value.find((i) => i.value === project.value))
// The server counts characters (code points) of the trimmed message.
const messageLength = computed(() => [...message.value.trim()].length)

// Preselect ?project= once the projects have loaded, when it is offered.
watch(
  offered,
  (list) => {
    const want = route.query.project
    if (!selected.value && typeof want === 'string' && list.some((i) => i.value === want)) project.value = want
  },
  { immediate: true },
)

const canSubmit = computed(() => !sending.value && !!selected.value && messageLength.value > 0 && messageLength.value <= maxMessage)

async function submit() {
  const p = selected.value
  if (!canSubmit.value || !p) return
  for (const k of fields) delete errors[k]
  formError.value = null
  sending.value = true
  try {
    const t = title.value.trim()
    const plan = await createPlan(p.namespace, p.name, { message: message.value, ...(t ? { title: t } : {}) })
    await router.push({ name: 'plans', params: { namespace: p.namespace, name: p.name, plan: plan.name } })
  } catch (e) {
    if (e instanceof ApiError && (fields as readonly string[]).includes(e.field ?? '')) errors[e.field as Field] = e.message
    else formError.value = errorMessage(e)
  } finally {
    sending.value = false
  }
}

// Shared looks with the research form: 16 px and 44 px on phones, the
// app's 14 px from a tablet on.
const fieldUI = { label: 'font-semibold text-[13px]', hint: 'text-[13px]', error: 'text-[13px] [overflow-wrap:anywhere]', help: 'text-[13px]' }
const control = 'text-base/5 tablet:text-sm/5'
</script>

<template>
  <div class="page">
    <UPageHeader title="New plan" :ui="pageHeaderUi" />

    <UAlert v-if="loadError" :color="alertColor('danger')" variant="subtle" class="mt-3 [overflow-wrap:anywhere]" :description="loadError" />
    <div v-else-if="!overview" class="card empty">Loading…</div>
    <UEmpty
      v-else-if="!offered.length"
      icon="i-lucide-map"
      title="You may not start plans in any project."
      class="section"
      data-plan-new-empty
    />

    <form
      v-else
      class="card mt-[18px] flex max-w-[820px] min-w-0 flex-col gap-[18px] px-4 py-4 tablet:px-5 tablet:py-[18px]"
      data-plan-new-form
      novalidate
      @submit.prevent="submit"
    >
      <UFormField label="Project" name="project" required :error="errors.project" :ui="fieldUI">
        <USelectMenu
          v-model="project"
          :items="items"
          value-key="value"
          :filter-fields="['label', 'description']"
          placeholder="Pick a project"
          :search-input="{ placeholder: 'Search projects…', ui: { base: control } }"
          trailing-icon="i-lucide-chevron-down"
          selected-icon="i-lucide-check"
          class="min-h-11 w-full tablet:min-h-0"
          :ui="{ base: control, item: 'min-h-11 tablet:min-h-0' }"
          data-plan-project
        />
      </UFormField>

      <UFormField label="What do you want to plan?" name="message" required :error="errors.message" :ui="fieldUI">
        <UTextarea
          v-model="message"
          :rows="5"
          autoresize
          placeholder="e.g. Add a CSV export to the Metrics page"
          class="w-full"
          :ui="{ base: control }"
          @keydown.meta.enter="submit"
          @keydown.ctrl.enter="submit"
        />
        <template #hint>
          <span :class="messageLength > maxMessage ? 'text-(--tone-danger-fg)' : 'text-(--text-3)'" data-counter>
            {{ messageLength }} / {{ maxMessage }}
          </span>
        </template>
      </UFormField>

      <UFormField label="Title" name="title" hint="Optional; the agent names the plan otherwise" :error="errors.title" :ui="fieldUI">
        <UInput v-model="title" autocomplete="off" class="w-full" :ui="{ base: control }" />
      </UFormField>

      <div class="flex flex-col gap-3 tablet:flex-row tablet:flex-wrap tablet:items-center">
        <UAlert
          v-if="formError"
          :color="alertColor('danger')"
          variant="subtle"
          class="[overflow-wrap:anywhere] tablet:flex-1"
          :description="formError"
          role="alert"
        />
        <UButton
          type="submit"
          :disabled="!canSubmit"
          :loading="sending"
          label="Start plan"
          class="min-h-11 justify-center tablet:ms-auto tablet:min-h-0"
        />
      </div>
    </form>
  </div>
</template>
