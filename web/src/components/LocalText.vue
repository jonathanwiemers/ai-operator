<script lang="ts">
import { defineComponent } from 'vue'
import { useNow } from '../live'
import { localTimeNodes } from '../localTime'

// LocalText is server text printed without Markdown, with each UTC time
// ("2026-11-21 14:05 UTC") as a local <time>. It renders no wrapper element:
// callers keep their own span or p and its classes (truncate, wrap).
export default defineComponent({
  name: 'LocalText',
  props: { text: { type: String, default: '' } },
  setup(props) {
    const now = useNow()
    return () => localTimeNodes(props.text, now.value)
  },
})
</script>
