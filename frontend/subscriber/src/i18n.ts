import { computed, ref, watchEffect } from 'vue'

// Hand-rolled rather than vue-i18n: the page has a couple of dozen strings and
// one plural, and vue-i18n's default plural rule is English-shaped anyway, so
// Russian would still come down to Intl.PluralRules.
//
// The panel's own locale choice cannot be reused: it lives in localStorage on
// the panel's origin, and this page is served by the sub server on another
// port -- a different origin. The language set and its order match the panel's
// LangMenu, so a customer sees the same choices the operator does.

export type Lang = 'en' | 'fa' | 'vi' | 'zhHans' | 'zhHant' | 'ru'

// tag is the BCP 47 form: what <html lang> and Intl.PluralRules want.
export const LANGS: { value: Lang; title: string; tag: string }[] = [
  { value: 'en', title: 'English', tag: 'en' },
  { value: 'fa', title: 'فارسی', tag: 'fa' },
  { value: 'vi', title: 'Tiếng Việt', tag: 'vi' },
  { value: 'zhHans', title: '简体中文', tag: 'zh-CN' },
  { value: 'zhHant', title: '繁體中文', tag: 'zh-TW' },
  { value: 'ru', title: 'Русский', tag: 'ru' },
]

const RTL = new Set<Lang>(['fa'])

type Plural = Partial<Record<Intl.LDMLPluralRule, string>> & { other: string }

const en = {
  subscription: 'Subscription',
  language: 'Language',
  active: 'Active',
  expired: 'Expired',
  disabled: 'Disabled',
  unknown: 'Unknown',
  loading: 'Loading…',
  loadFailed: 'Could not load subscription info.',
  inactive: 'This subscription is not active. Contact your provider.',
  remaining: 'Remaining',
  used: 'Used',
  total: 'Total',
  left: 'Left',
  days: { one: '{n} day', other: '{n} days' } as Plural,
  unlimited: 'Unlimited',
  configLinks: 'Config Links',
  copy: 'Copy',
  copied: 'Copied ✓',
  linksHint: 'Copy a link and paste it into your client app, or import the subscription URL directly.',
  noLinks: 'No config links available.',
  poweredBy: 'Powered by 2s-ui',
}

type Messages = typeof en

// Typed against en, so a key missing from any language fails vue-tsc -- the
// only check this project has.
const MESSAGES: Record<Lang, Messages> = {
  en,
  fa: {
    subscription: 'اشتراک',
    language: 'زبان',
    active: 'فعال',
    expired: 'منقضی شده',
    disabled: 'غیرفعال',
    unknown: 'نامشخص',
    loading: 'در حال بارگذاری…',
    loadFailed: 'دریافت اطلاعات اشتراک ممکن نشد.',
    inactive: 'این اشتراک فعال نیست. با ارائه‌دهنده خود تماس بگیرید.',
    remaining: 'زمان باقی‌مانده',
    used: 'مصرف‌شده',
    total: 'کل',
    left: 'حجم باقی‌مانده',
    days: { other: '{n} روز' },
    unlimited: 'نامحدود',
    configLinks: 'لینک‌های پیکربندی',
    copy: 'کپی',
    copied: 'کپی شد ✓',
    linksHint: 'یک لینک را کپی کنید و در برنامه کلاینت خود وارد کنید، یا آدرس اشتراک را مستقیماً وارد کنید.',
    noLinks: 'هیچ لینک پیکربندی‌ای موجود نیست.',
    poweredBy: 'قدرت‌گرفته از 2s-ui',
  },
  vi: {
    subscription: 'Gói đăng ký',
    language: 'Ngôn ngữ',
    active: 'Hoạt động',
    expired: 'Đã hết hạn',
    disabled: 'Đã vô hiệu hóa',
    unknown: 'Không rõ',
    loading: 'Đang tải…',
    loadFailed: 'Không thể tải thông tin gói đăng ký.',
    inactive: 'Gói đăng ký này không hoạt động. Vui lòng liên hệ nhà cung cấp.',
    remaining: 'Thời hạn còn lại',
    used: 'Đã dùng',
    total: 'Tổng',
    left: 'Dung lượng còn lại',
    days: { other: '{n} ngày' },
    unlimited: 'Không giới hạn',
    configLinks: 'Liên kết cấu hình',
    copy: 'Sao chép',
    copied: 'Đã sao chép ✓',
    linksHint: 'Sao chép một liên kết và dán vào ứng dụng, hoặc nhập trực tiếp địa chỉ gói đăng ký.',
    noLinks: 'Không có liên kết cấu hình nào.',
    poweredBy: 'Được cung cấp bởi 2s-ui',
  },
  zhHans: {
    subscription: '订阅',
    language: '语言',
    active: '有效',
    expired: '已到期',
    disabled: '已停用',
    unknown: '未知',
    loading: '加载中…',
    loadFailed: '无法加载订阅信息。',
    inactive: '此订阅未启用，请联系你的服务提供方。',
    remaining: '剩余时间',
    used: '已用流量',
    total: '总流量',
    left: '剩余流量',
    days: { other: '{n} 天' },
    unlimited: '不限',
    configLinks: '配置链接',
    copy: '复制',
    copied: '已复制 ✓',
    linksHint: '复制链接后粘贴到客户端，或直接导入订阅地址。',
    noLinks: '暂无可用的配置链接。',
    poweredBy: '由 2s-ui 驱动',
  },
  zhHant: {
    subscription: '訂閱',
    language: '語言',
    active: '有效',
    expired: '已到期',
    disabled: '已停用',
    unknown: '未知',
    loading: '載入中…',
    loadFailed: '無法載入訂閱資訊。',
    inactive: '此訂閱未啟用，請聯絡你的服務提供者。',
    remaining: '剩餘時間',
    used: '已用流量',
    total: '總流量',
    left: '剩餘流量',
    days: { other: '{n} 天' },
    unlimited: '不限',
    configLinks: '設定連結',
    copy: '複製',
    copied: '已複製 ✓',
    linksHint: '複製連結後貼到用戶端，或直接匯入訂閱網址。',
    noLinks: '目前沒有可用的設定連結。',
    poweredBy: '由 2s-ui 驅動',
  },
  ru: {
    subscription: 'Подписка',
    language: 'Язык',
    active: 'Активна',
    expired: 'Истекла',
    disabled: 'Отключена',
    unknown: 'Неизвестно',
    loading: 'Загрузка…',
    loadFailed: 'Не удалось загрузить данные подписки.',
    inactive: 'Подписка неактивна. Обратитесь к своему поставщику.',
    remaining: 'Срок',
    used: 'Использовано',
    total: 'Всего',
    left: 'Остаток',
    days: { one: '{n} день', few: '{n} дня', many: '{n} дней', other: '{n} дня' },
    unlimited: 'Безлимит',
    configLinks: 'Ссылки конфигурации',
    copy: 'Копировать',
    copied: 'Скопировано ✓',
    linksHint: 'Скопируйте ссылку и вставьте её в приложение-клиент или импортируйте адрес подписки целиком.',
    noLinks: 'Нет доступных ссылок конфигурации.',
    poweredBy: 'Работает на 2s-ui',
  },
}

const STORAGE_KEY = '2sui-sub-lang'

// matchLang accepts both the panel's own keys (zhHans) and BCP 47 tags
// (zh-CN, zh-Hant-HK, ru-RU), so ?lang= takes whatever an operator is likely
// to type and navigator.languages goes through the same path.
export function matchLang(raw: string | null | undefined): Lang | null {
  if (!raw) return null
  const tag = raw.trim().toLowerCase().replace(/_/g, '-')
  const exact = LANGS.find((l) => l.value.toLowerCase() === tag)
  if (exact) return exact.value
  const [primary, ...rest] = tag.split('-')
  if (primary === 'zh') {
    // The script subtag outranks the region: zh-Hans-HK is Simplified.
    if (rest.includes('hans')) return 'zhHans'
    if (rest.some((s) => s === 'hant' || s === 'tw' || s === 'hk' || s === 'mo')) return 'zhHant'
    return 'zhHans'
  }
  return LANGS.find((l) => l.value === primary)?.value ?? null
}

// Storage can throw outright (blocked site data, some private modes); the
// language is a convenience, so a failure there just means it is not kept.
function readStored(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

function writeStored(l: Lang) {
  try {
    localStorage.setItem(STORAGE_KEY, l)
  } catch {
    // see readStored
  }
}

// An explicit ?lang= wins, so an operator can hand out a link in the
// customer's language; then the customer's own earlier pick; then the browser.
function initialLang(): Lang {
  const fromUrl = matchLang(new URLSearchParams(window.location.search).get('lang'))
  if (fromUrl) return fromUrl
  const stored = matchLang(readStored())
  if (stored) return stored
  const prefs = navigator.languages?.length ? navigator.languages : [navigator.language]
  for (const tag of prefs) {
    const m = matchLang(tag)
    if (m) return m
  }
  return 'en'
}

const lang = ref<Lang>(initialLang())

watchEffect(() => {
  const root = document.documentElement
  root.lang = LANGS.find((l) => l.value === lang.value)!.tag
  root.dir = RTL.has(lang.value) ? 'rtl' : 'ltr'
})

function setLang(l: Lang) {
  lang.value = l
  writeStored(l)
  // ?lang= outranks the stored pick, so leaving it in the address would undo
  // the choice on the next reload. Rewrite it rather than drop it: the address
  // may be bookmarked or shared, and it should keep opening in this language.
  const url = new URL(window.location.href)
  if (url.searchParams.has('lang')) {
    url.searchParams.set('lang', l)
    history.replaceState(history.state, '', url)
  }
}

export function useI18n() {
  const t = computed(() => MESSAGES[lang.value])
  const plural = computed(() => new Intl.PluralRules(LANGS.find((l) => l.value === lang.value)!.tag))
  function days(n: number): string {
    const forms = t.value.days
    return (forms[plural.value.select(n)] ?? forms.other).replace('{n}', String(n))
  }
  return { lang, t, days, setLang }
}
