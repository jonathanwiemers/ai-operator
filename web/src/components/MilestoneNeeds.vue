<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { chatPostError } from '../api'
import { postTaskChat } from '../client'
import { needsOpen } from '../needsOpen'
import { promptAnchor } from '../promptCard'
import type { MilestoneView } from '../types'
import Markdown from './Markdown.vue'
import PauseCard from './PauseCard.vue'
import PausesCard from './PausesCard.vue'
import PromptCard from './PromptCard.vue'
import RelTime from './RelTime.vue'

// MilestoneNeeds is a milestone's Needs you tab (R12). Open (live
// milestones only): the full pause card while paused, one card per held
// issue, the open prompts without the pause card's own, and the pilot's
// open older proposals (P-n) with Accept and Reject. Handled: the closed
// prompts folded under Earlier questions and the pause history. A stored
// milestone shows only Handled.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView }>()

const open = computed(() => needsOpen(props.milestone))
const active = computed(() => !props.milestone.stored)
// A Desk link to a closed prompt unfolds the closed ones.
const route = useRoute()
const closedLinked = computed(() => open.value.closed.some((p) => route.hash === `#${promptAnchor(p.id)}`))

// Accept and Reject send the pilot task the same chat command as the chat's
// buttons did; only owners with the chat grant get them.
const canAnswer = computed(() => !!props.milestone.permissions?.chat && !!props.milestone.pilot)
const sending = ref<number | null>(null)
const sendError = ref('')
async function decide(cmd: 'accept' | 'reject', proposal: number) {
  const task = props.milestone.pilot?.task
  if (!task || sending.value != null) return
  sending.value = proposal
  sendError.value = ''
  try {
    await postTaskChat(props.namespace, props.name, task, {
      body: JSON.stringify({ cmd, proposal }),
      clientKey: crypto.randomUUID(),
      kind: 'command',
    })
  } catch (e) {
    sendError.value = chatPostError(e)
  } finally {
    sending.value = null
  }
}

// Utilities only (docs/contributing/web-ui.md, "Needs you tab"). On phones
// the Earlier questions fold is 44 px high; the cards bring their own
// touch sizes.
const sectionHead =
  'm-0 mb-2 flex justify-between gap-2 text-[11.5px] font-semibold tracking-[0.06em] text-(--text-3) uppercase'
const quiet = 'text-[13px] text-(--text-3)'
const chip = { color: 'neutral', variant: 'outline', size: 'sm' } as const
</script>

<template>
  <div class="grid max-w-[860px] min-w-0 gap-[18px]">
    <section v-if="active" class="min-w-0" data-needs-open>
      <h3 :class="sectionHead">
        <span>Open</span>
        <span v-if="open.count">{{ open.count }}</span>
      </h3>
      <div class="grid gap-2.5">
        <div v-if="!open.count" :class="quiet">Nothing needs you.</div>
        <PauseCard v-if="open.paused" :key="milestone.iid" :namespace="namespace" :name="name" :milestone="milestone" />
        <PauseCard
          v-for="h in open.holds"
          :key="`${milestone.iid}/${h.iid}`"
          :namespace="namespace"
          :name="name"
          :milestone="milestone"
          :hold="h"
        />
        <PromptCard v-for="p in open.prompts" :key="p.id" :namespace="namespace" :name="name" :prompt="p" />
        <div
          v-for="p in open.proposals"
          :key="`P${p.id}`"
          class="card grid gap-2 px-3.5 py-3 text-[13.5px]"
          :data-proposal="p.id"
        >
          <div class="flex flex-wrap items-baseline gap-x-2">
            <b>🧭 P{{ p.id }}</b>
            <RelTime v-if="p.postedAt" class="faint ml-auto text-[12px]" :time="p.postedAt" />
          </div>
          <!-- The summary is the pilot's untrusted text: markdown without HTML. -->
          <Markdown class="min-w-0 wrap-anywhere" :text="p.summary" />
          <div v-if="canAnswer" class="flex flex-wrap gap-1.5">
            <UButton v-bind="chip" label="Accept" :disabled="sending != null" @click="decide('accept', p.id)" />
            <UButton v-bind="chip" label="Reject" :disabled="sending != null" @click="decide('reject', p.id)" />
          </div>
        </div>
        <div v-if="sendError" class="banner danger">{{ sendError }}</div>
      </div>
    </section>

    <section class="min-w-0" data-needs-handled>
      <h3 :class="sectionHead"><span>Handled</span></h3>
      <div class="grid gap-2.5">
        <div v-if="!open.closed.length && !milestone.pauses" :class="quiet">Nothing handled yet.</div>
        <details v-if="open.closed.length" class="group" :open="closedLinked" data-earlier>
          <summary class="min-h-11 cursor-pointer py-3 text-[13px] text-(--text-2) tablet:min-h-0 tablet:py-0 group-open:mb-2.5">
            Earlier questions ({{ open.closed.length }})
          </summary>
          <div class="grid gap-2.5">
            <PromptCard v-for="p in open.closed" :key="p.id" :namespace="namespace" :name="name" :prompt="p" />
          </div>
        </details>
        <PausesCard v-if="milestone.pauses" :pauses="milestone.pauses" />
      </div>
    </section>
  </div>
</template>
