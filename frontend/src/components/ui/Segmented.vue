<template>
  <div class="segmented" :class="{ block }">
    <div
      ref="track"
      class="seg-track hide-scroll"
      :class="{ 'fade-l': hidden.left, 'fade-r': hidden.right }"
      @scroll.passive="measure"
      @scrollend="settle"
      @wheel="onWheel"
    >
      <button
        v-for="[v, l] in options"
        :key="String(v)"
        type="button"
        :class="{ active: modelValue === v }"
        @click="$emit('update:modelValue', v)"
      >{{ l }}</button>
    </div>
    <!-- a mouse affordance only: Tab already reaches every option, and
         mousedown is kept from moving focus onto an arrow that may vanish -->
    <button
      v-if="hidden.left"
      type="button"
      class="seg-arrow l"
      tabindex="-1"
      aria-hidden="true"
      @mousedown.prevent
      @click="turn(-1)"
    ><Ico name="chevron" :size="14" style="transform: rotate(180deg);" /></button>
    <button
      v-if="hidden.right"
      type="button"
      class="seg-arrow r"
      tabindex="-1"
      aria-hidden="true"
      @mousedown.prevent
      @click="turn(1)"
    ><Ico name="chevron" :size="14" /></button>
  </div>
</template>

<script lang="ts" setup>
import { nextTick, onBeforeUnmount, onMounted, onUpdated, reactive, ref, watch } from 'vue'
import Ico from './Ico.vue'

const props = defineProps<{
  modelValue: string | number
  // [value, label] pairs
  options: [string | number, string][]
  block?: boolean
}>()
defineEmits<{ 'update:modelValue': [value: string | number] }>()

// how far in from an edge the arrow and its fade reach, matching the
// .seg-track masks in app.css
const FADE = 52

const track = ref<HTMLElement>()
// which physical edges have options scrolled out of view
const hidden = reactive({ left: false, right: false })

const isRtl = (el: HTMLElement) => getComputedStyle(el).direction === 'rtl'
const overflows = (el: HTMLElement) => el.scrollWidth > el.clientWidth + 1

const measure = () => {
  const el = track.value
  if (!el) return
  const max = el.scrollWidth - el.clientWidth
  // scrollLeft runs 0..max in ltr and -max..0 in rtl
  const lo = isRtl(el) ? -max : 0
  hidden.left = max > 1 && el.scrollLeft > lo + 1
  hidden.right = max > 1 && el.scrollLeft < lo + max - 1
}

// Chrome's compositor rounds a fractional scroll range down (454.4px wide at
// 125% zoom: 100.8 here, 100 there), and a bar left on that fraction swallows
// the next wheel notch snapping back into range instead of passing it on to
// the drawer. Whole pixels keep it in range; the move is under 1px.
const settle = () => {
  const el = track.value
  if (el && el.scrollLeft % 1) el.scrollLeft = Math.trunc(el.scrollLeft)
}

// the scrollbar is hidden, and a plain mouse wheel only scrolls vertically —
// without this an option past the edge is unreachable from a desktop mouse
const onWheel = (e: WheelEvent) => {
  const el = track.value
  // trackpads already scroll sideways natively, and ctrl+wheel is zoom
  if (!el || e.ctrlKey || Math.abs(e.deltaX) >= Math.abs(e.deltaY) || !overflows(el)) return
  measure()
  // a notch can arrive in lines (Firefox) or pages instead of pixels
  const dy = e.deltaY * (e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? el.clientWidth : 1)
  // forward walks toward the last option, which sits on the left in rtl
  const dx = isRtl(el) ? -dy : dy
  // once the bar can't move that way, the wheel goes back to the page
  if (dx > 0 ? !hidden.right : !hidden.left) return
  e.preventDefault()
  el.scrollLeft += dx
}

// an arrow turns a page: what fits between the two faded edges. The arrows
// sit on physical sides, and positive scrollLeft is rightwards in rtl too.
const turn = (dir: 1 | -1) => {
  const el = track.value
  if (el) el.scrollBy({ left: dir * Math.max(el.clientWidth - 2 * FADE, FADE), behavior: 'smooth' })
}

// bring the selected option clear of the fade by scrolling this bar only —
// scrollIntoView would also scroll the drawer around it
const reveal = (behavior: ScrollBehavior) => {
  const el = track.value
  const btn = el?.querySelector<HTMLElement>('button.active')
  if (!el || !btn || !overflows(el)) return
  const c = el.getBoundingClientRect()
  const b = btn.getBoundingClientRect()
  // rects are scaled while a drawer animates in; scroll offsets are not
  const k = c.width / el.offsetWidth || 1
  const pad = FADE * k
  let d = 0
  if (b.left < c.left + pad) d = b.left - c.left - pad
  else if (b.right > c.right - pad) d = b.right - c.right + pad
  if (d) el.scrollBy({ left: d / k, behavior })
}

watch(() => props.modelValue, () => nextTick(() => reveal('smooth')))

// the track resizes with its container, the buttons with their labels (font
// load, locale switch); options themselves can change (client groups)
let ro: ResizeObserver | undefined
const observe = () => {
  const el = track.value
  if (!ro || !el) return
  ro.disconnect()
  ro.observe(el)
  for (const b of el.children) ro.observe(b)
}

onMounted(() => {
  ro = new ResizeObserver(() => measure())
  observe()
  reveal('auto')
})
onUpdated(observe)
onBeforeUnmount(() => ro?.disconnect())
</script>
