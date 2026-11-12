import { onBeforeUnmount, onMounted, shallowRef, type ShallowRef } from 'vue'
import { readChartColors, type ChartColors } from '../../chartData'

// The chart colours (docs/ui.md, "Charts"): style.css's --chart-* and text
// and grid tokens, read from <html> at runtime, since a canvas takes colours,
// not var(). colorScheme.ts toggles `dark` on <html> when the system scheme
// changes; a MutationObserver on its class re-reads them, so a drawn chart
// follows light and dark.
function read(): ChartColors {
  const style = getComputedStyle(document.documentElement)
  return readChartColors((name) => style.getPropertyValue(name))
}

export function useChartColors(): ShallowRef<ChartColors> {
  const colors = shallowRef(read())
  const observer = new MutationObserver(() => {
    const next = read()
    // Other classes on <html> change nothing: keep the chart as it is.
    if (JSON.stringify(next) !== JSON.stringify(colors.value)) colors.value = next
  })
  onMounted(() => observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] }))
  onBeforeUnmount(() => observer.disconnect())
  return colors
}
