import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import HttpUtils from '@/plugins/httputil'
import { EntityRow } from '@/components/ui/EntityCard.vue'

// The URL the delay test fetches through each outbound. Off by default, which
// leaves it to sing-box (gstatic's generate_204). Kept per browser rather than
// as a panel setting: it is a diagnostic choice for whoever is looking, and the
// scheduled outbound probe has its own (notifyOutboundUrl). Module-level, so
// the Outbounds and Endpoints pages share one choice.
const TEST_URL_ON_KEY = '2sui-urltest-on'
const TEST_URL_KEY = '2sui-urltest-url'

const readStored = (key: string): string | null => {
  try { return localStorage.getItem(key) } catch { return null }
}
const writeStored = (key: string, value: string) => {
  try { localStorage.setItem(key, value) } catch { /* private mode */ }
}

const testUrlEnabled = ref(readStored(TEST_URL_ON_KEY) === 'true')
const testUrl = ref(readStored(TEST_URL_KEY) ?? '')
watch(testUrlEnabled, (v) => writeStored(TEST_URL_ON_KEY, v ? 'true' : 'false'))
watch(testUrl, (v) => writeStored(TEST_URL_KEY, v))

export const isValidTestUrl = (s: string): boolean => {
  try {
    const u = new URL(s)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host !== ''
  } catch {
    return false
  }
}

// What a test sends as `link`, or undefined for sing-box's default. An invalid
// URL is not sent: the field shows why, rather than every test failing with an
// error that does not point at it.
const testLink = computed((): string | undefined =>
  testUrlEnabled.value && isValidTestUrl(testUrl.value) ? testUrl.value : undefined)

export function useTestUrl() {
  return { testUrlEnabled, testUrl, testLink }
}

interface CheckResult {
  loading?: boolean
  success: boolean
  data?: { OK?: boolean; Delay?: number; Error?: string } | null
  errorMessage?: string
}

// Outbounds and endpoints both probe through api/checkOutbound — endpoints
// register as outbounds inside the core — so the whole flow was duplicated
// between the two views.
//
// `list` is a getter rather than a Ref because both callers hold a readonly
// computed; taking Ref would type-check and then fail to track. It is any[]
// rather than {tag: string}[] because Outbound and Endpoint resolve to
// `{ type: string; [k: string]: any }` — their InterfaceMap is a mapped type
// that never references the per-protocol interfaces — so a required `tag`
// bound does not hold.
export function useDelayCheck(list: () => any[]) {
  const { t } = useI18n({ useScope: 'global' })

  const checkResults = ref<Record<string, CheckResult>>({})
  const testingAll = ref(false)

  const check = async (tag: string) => {
    checkResults.value = { ...checkResults.value, [tag]: { loading: true, success: false } }
    const msg = await HttpUtils.get('api/checkOutbound', { tag, link: testLink.value })
    const success = msg.success && msg.obj?.OK
    const errorMessage = success ? undefined : (msg.obj?.Error ?? msg.msg ?? '')
    checkResults.value = {
      ...checkResults.value,
      [tag]: { loading: false, success, data: msg.obj ?? null, errorMessage },
    }
  }

  const checkAll = async () => {
    const items = list()
    if (items.length === 0) return
    testingAll.value = true
    try {
      await Promise.all(items.map((i) => check(i.tag)))
    } finally {
      testingAll.value = false
    }
  }

  const delayRow = (tag: string): EntityRow => {
    const r = checkResults.value[tag]
    if (r?.loading) return { k: t('out.delay'), v: '…', mono: true }
    if (r && r.loading == false) {
      if (r.success) {
        return { k: t('out.delay'), v: (r.data?.Delay ?? 0) + ' ' + t('date.ms'), mono: true, color: 'var(--emerald)' }
      }
      return { k: t('out.delay'), v: r.errorMessage || t('failed'), color: 'var(--rose)' }
    }
    return { k: t('out.delay'), v: t('ui.none') }
  }

  return { checkResults, testingAll, check, checkAll, delayRow }
}
