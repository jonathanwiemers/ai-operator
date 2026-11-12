<script setup lang="ts">
import Markdown from './Markdown.vue'
import { companionRef } from '../companion'
import type { MilestoneView } from '../types'
import Truncated from '../ui/Truncated.vue'
import CompanionChip from './CompanionChip.vue'
import ExtLink from './ExtLink.vue'

// CompanionsList: a milestone's companion merge requests, each with its home
// issue (its implement task, else the GitLab issue). Reasons are untrusted
// text; a clipped reason opens on tap.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView }>()

const homeTask = (iid: number) =>
  props.milestone.tasks.find((t) => t.type === 'Implement' && t.issue?.iid === iid)?.name
const issueURL = (iid: number) => props.milestone.webURL.replace(/\/-\/milestones\/\d+$/, `/-/issues/${iid}`)
</script>

<template>
  <!-- One minmax(0, 1fr) column: the one-line reason is cut at the column, it does not set its width. -->
  <ul class="m-0 grid list-none grid-cols-[minmax(0,1fr)] gap-2.5 p-0 text-[13px]">
    <li v-for="c in milestone.companions" :key="`${c.homeIssueIID}/${companionRef(c)}`">
      <div class="row">
        <ExtLink :href="c.mrURL" class="mono min-w-0 wrap-anywhere">{{ companionRef(c) }}</ExtLink>
        <span class="spacer" />
        <CompanionChip :companion="c" plain />
      </div>
      <div class="row faint">
        <RouterLink v-if="homeTask(c.homeIssueIID)" :to="{ name: 'task', params: { namespace, name, task: homeTask(c.homeIssueIID)! } }">
          #{{ c.homeIssueIID }}
        </RouterLink>
        <ExtLink v-else :href="issueURL(c.homeIssueIID)">#{{ c.homeIssueIID }}</ExtLink>
        <span v-if="c.release">release {{ c.release }}</span>
        <Truncated v-if="c.reason" class="min-w-0"><Markdown mode="inline" :text="c.reason" /></Truncated>
      </div>
    </li>
  </ul>
</template>
