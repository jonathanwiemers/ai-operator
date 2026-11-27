<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UCard from '@nuxt/ui/components/Card.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UProgress from '@nuxt/ui/components/Progress.vue'
import Badge from './Badge.vue'
import RelTime from './RelTime.vue'
import { progressBar, progressUI, stateTone } from '../milestoneRows'
import type { ProjectCard } from '../projectCards'

// The Overview's Projects tab (R5 of "Desk: Milestones and Issues first, a
// Timeline tab, and what each project builds", D4): one card per project
// from projectCards, with its needs-you count, what it builds now (each
// building milestone with its progress and running issues), up to three
// queued milestones, and the finishing and planned counts; the idle
// projects fold into one line below. Titles are untrusted: text only.
defineProps<{ cards: ProjectCard[]; idle: ProjectCard[] }>()

type P = Pick<ProjectCard, 'namespace' | 'name'>
const projectTo = (p: P, tab?: string) => ({
  name: 'stream-project' as const,
  params: { namespace: p.namespace, name: p.name },
  query: tab ? { tab } : {},
})
const milestoneTo = (p: P, iid: number) => ({
  name: 'stream-milestone' as const,
  params: { namespace: p.namespace, name: p.name, iid: String(iid) },
})
// A running issue's link: its thread in the milestone's Cockpit, a loose
// issue's the project's Loose issues tab (D899-P2).
const issueTo = (p: P, milestoneIID: number | undefined, iid: number) =>
  milestoneIID ? { ...milestoneTo(p, milestoneIID), query: { tab: 'cockpit' }, hash: `#issue-${iid}` } : projectTo(p, 'loose')
type Group = ProjectCard['runningIssues'][number]
// Building now's rows: each building milestone with its running issues,
// then the running issues of other milestones and the loose ones.
const groups = (c: ProjectCard) => [
  ...c.building.map((m) => ({ key: m.iid, iid: m.iid, title: m.title || `%${m.iid}`, milestone: m, issues: c.runningIssues.find((g) => g.milestoneIID === m.iid)?.issues ?? [] })),
  ...c.runningIssues
    .filter((g: Group) => !c.building.some((m) => m.iid === g.milestoneIID))
    .map((g: Group) => ({ key: g.milestoneIID ?? 'loose', iid: g.milestoneIID, title: g.title, milestone: undefined, issues: g.issues })),
]
const bar = (m: ProjectCard['building'][number]) => progressBar(m.progress)
const label = (s: string) => (s === 'paused' ? 'Paused' : 'Running')

// A link's look: below 760 px at least 44 px high.
const link = { color: 'neutral', variant: 'link', class: 'min-h-11 max-w-full px-0 tablet:min-h-0' } as const
const head = 'm-0 mb-1 text-xs font-semibold text-(--text-3)'
</script>

<template>
  <div class="grid gap-4 desktop:grid-cols-2" data-project-cards>
    <UCard v-for="c in cards" :key="`${c.namespace}/${c.name}`" :ui="{ body: 'flex flex-col gap-3' }" :data-project-card="`${c.namespace}/${c.name}`">
      <template #header>
        <div class="flex flex-wrap items-center gap-2">
          <h3 class="m-0 min-w-0 flex-1 text-[15px] [overflow-wrap:anywhere]">
            <UButton v-bind="link" :to="projectTo(c)" class="text-[15px] font-semibold" data-card-project>
              <span class="[overflow-wrap:anywhere]">{{ c.name }}</span>
            </UButton>
          </h3>
          <Badge v-if="c.suspended" tone="warning">Suspended</Badge>
          <UButton
            v-if="c.needsYou"
            :to="projectTo(c, 'cockpit')"
            color="warning"
            variant="soft"
            size="xs"
            class="min-h-11 rounded-full tablet:min-h-0"
            :label="`${c.needsYou} need${c.needsYou === 1 ? 's' : ''} you`"
            data-card-needs-you
          />
        </div>
      </template>

      <section data-card-building>
        <h4 :class="head">Building now</h4>
        <UEmpty v-if="!c.building.length && !c.runningIssues.length" icon="i-lucide-pause" title="Nothing running" variant="naked" size="xs" :ui="{ root: 'items-start p-0' }" />
        <div v-for="g in groups(c)" :key="g.key" class="flex flex-col gap-1" :data-card-milestone="g.iid">
          <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <UButton v-if="g.iid" v-bind="link" :to="milestoneTo(c, g.iid)" class="min-w-0 font-semibold [overflow-wrap:anywhere]">{{ g.title }}</UButton>
            <span v-else class="text-[13px] font-semibold">{{ g.title }}</span>
            <template v-if="g.milestone">
              <Badge :tone="stateTone(g.milestone.state)">{{ label(g.milestone.state) }}</Badge>
              <span class="flex min-w-32 flex-1 items-center gap-2">
                <UProgress v-bind="progressUI" :model-value="bar(g.milestone).value" :max="bar(g.milestone).max" :get-value-label="() => bar(g.milestone!).label" />
                <span class="text-xs whitespace-nowrap text-(--text-2) tabular-nums">{{ g.milestone.progress.merged }}/{{ g.milestone.progress.total }}</span>
              </span>
            </template>
          </div>
          <ul v-if="g.issues.length" class="m-0 flex flex-col p-0 ps-4">
            <li v-for="i in g.issues" :key="i.iid">
              <UButton v-bind="link" :to="issueTo(c, g.iid, i.iid)" class="text-[13px] font-normal" :data-card-issue="i.iid">
                <span class="[overflow-wrap:anywhere]">#{{ i.iid }} {{ i.title }}<template v-if="i.step"> · {{ i.step }}</template><template v-if="i.stepSince"> · <RelTime :time="i.stepSince" /></template></span>
              </UButton>
            </li>
          </ul>
        </div>
      </section>

      <section v-if="c.upNext.length" data-card-next>
        <h4 :class="head">Up next</h4>
        <ol class="m-0 flex flex-col p-0">
          <li v-for="m in c.upNext" :key="m.iid">
            <UButton v-bind="link" :to="milestoneTo(c, m.iid)" class="text-[13px] font-normal">
              <span class="[overflow-wrap:anywhere]">{{ m.queuePosition }}. {{ m.title || `%${m.iid}` }}</span>
            </UButton>
          </li>
        </ol>
        <UButton v-if="c.moreQueued" v-bind="link" :to="projectTo(c)" class="text-[13px]" :label="`${c.moreQueued} more`" data-card-more />
      </section>

      <template v-if="c.finishing || c.planned" #footer>
        <div class="flex flex-wrap gap-x-4 text-[13px]" data-card-footer>
          <UButton v-if="c.finishing" v-bind="link" :to="projectTo(c)" :label="`${c.finishing} finishing`" />
          <UButton v-if="c.planned" v-bind="link" :to="projectTo(c)" :label="`${c.planned} planned without autopilot`" />
        </div>
      </template>
    </UCard>
  </div>
  <p v-if="idle.length" class="muted mt-3 mb-0 flex flex-wrap items-center gap-x-1 text-[13px]" data-projects-idle>
    <span>Idle:</span>
    <span v-for="(p, n) in idle" :key="`${p.namespace}/${p.name}`" class="inline-flex max-w-full items-center">
      <UButton v-bind="link" :to="projectTo(p)"><span class="[overflow-wrap:anywhere]">{{ p.name }}</span></UButton>{{ n < idle.length - 1 ? ',' : '' }}
    </span>
  </p>
</template>
