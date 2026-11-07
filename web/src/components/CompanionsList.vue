<script setup lang="ts">
import { companionRef } from '../companion'
import type { MilestoneView } from '../types'
import CompanionChip from './CompanionChip.vue'
import ExtLink from './ExtLink.vue'

// CompanionsList: a milestone's companion merge requests, each with its home
// issue (its implement task, else the GitLab issue). Reasons are untrusted
// text.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView }>()

const homeTask = (iid: number) =>
  props.milestone.tasks.find((t) => t.type === 'Implement' && t.issue?.iid === iid)?.name
const issueURL = (iid: number) => props.milestone.webURL.replace(/\/-\/milestones\/\d+$/, `/-/issues/${iid}`)
</script>

<template>
  <ul class="companions">
    <li v-for="c in milestone.companions" :key="`${c.homeIssueIID}/${companionRef(c)}`">
      <div class="row">
        <ExtLink :href="c.mrURL" class="mono ref">{{ companionRef(c) }}</ExtLink>
        <span class="spacer" />
        <CompanionChip :companion="c" plain />
      </div>
      <div class="row faint">
        <RouterLink v-if="homeTask(c.homeIssueIID)" :to="{ name: 'task', params: { namespace, name, task: homeTask(c.homeIssueIID)! } }">
          #{{ c.homeIssueIID }}
        </RouterLink>
        <ExtLink v-else :href="issueURL(c.homeIssueIID)">#{{ c.homeIssueIID }}</ExtLink>
        <span v-if="c.release">release {{ c.release }}</span>
        <span v-if="c.reason" class="reason" :title="c.reason">{{ c.reason }}</span>
      </div>
    </li>
  </ul>
</template>

<style scoped>
.companions {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 10px;
  font-size: 13px;
}
.companions .ref {
  min-width: 0;
  overflow-wrap: anywhere;
}
.companions .reason {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
