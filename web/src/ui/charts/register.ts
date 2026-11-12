// Chart.js for the Desk's charts (docs/ui.md, "Charts"): only what the
// wrappers use is registered, so the rest is tree-shaken out of the costs
// chunk. No legend plugin: the legend is HTML with values (ChartLegend.vue).
// Importing this module registers them; it is imported by the wrappers only,
// so Chart.js lands in the chunks of the views that draw charts.
import {
  BarController,
  BarElement,
  CategoryScale,
  Chart,
  LinearScale,
  LineController,
  LineElement,
  PointElement,
  Tooltip,
} from 'chart.js'

Chart.register(BarController, BarElement, CategoryScale, LinearScale, LineController, LineElement, PointElement, Tooltip)
