import Data from '@/store/modules/data'
import { HttpClientRef, HttpClientOptions } from '@/types/httpClient'

// sing-box refuses a detour to a direct outbound that carries no options of
// its own, as pointless: it dials exactly what a client with no detour dials.
// Such an outbound is therefore never offered as something to download over;
// DIRECT_HTTP_CLIENT is how "download directly" is said instead.
export function isNoopDetour(tag: string): boolean {
  if (!tag) return true
  const outbound = Data().outbounds?.find((o: any) => o.tag == tag)
  if (!outbound || outbound.type != 'direct') return false
  return Object.keys(outbound).every(key => ['id', 'type', 'tag'].includes(key))
}

// The inline client a remote rule-set downloads directly with. A client with
// no detour dials the system network itself, which is direct -- but one with
// nothing in it counts as not set, and a rule-set with no client falls back to
// the default one: with none declared, the implicit client riding the default
// outbound, which sing-box 1.14 deprecated. engine "go" is the documented
// default and changes nothing about how the download is made; it only keeps the
// object from being empty. util.DirectHTTPClient writes the same value.
export const DIRECT_HTTP_CLIENT: HttpClientOptions = { engine: 'go' }

// The tags of the shared clients declared in the config, for the selects that
// reference one.
export function httpClientTags(): string[] {
  return (Data().config?.http_clients ?? [])
    .map((client: any) => client.tag)
    .filter((tag: string) => tag?.length > 0)
}

// The client a rule-set naming none downloads with: route.default_http_client,
// or else the first declared client, which sing-box takes as the default when
// nothing is named. Empty when the config declares neither -- the case 1.14
// deprecated, and the one the panel refuses to save.
export function defaultHttpClientTag(): string {
  return Data().config?.route?.default_http_client || httpClientTags()[0] || ''
}

// An http_client is either the tag of a shared client or inline options.
export function refTag(value?: HttpClientRef): string | undefined {
  return typeof value === 'string' ? value : undefined
}

export function refDetour(value?: HttpClientRef): string | undefined {
  return value != undefined && typeof value === 'object' ? value.detour : undefined
}

// Whether sing-box reads an http_client as not set: HTTPClientOptions.IsEmpty
// compares the whole options struct against zero, so an object holding only
// zero values is as absent as no object. Mirrors util.IsEmptyHTTPClient.
function isZero(value: any): boolean {
  if (value == undefined || value === false || value === '' || value === 0) return true
  if (Array.isArray(value)) return value.length === 0
  if (typeof value === 'object') return Object.values(value).every(isZero)
  return false
}

// How a remote rule-set downloads, as the value of the one select that picks it:
//
//   'default'       no client of its own: the config's default client
//   'direct'        a client with no detour
//   'client:<tag>'  a shared client
//   'out:<tag>'     a client dialing through that outbound
//   'custom'        inline options the select does not model, kept as they are
//   ''              no client and no default to fall back on -- the deprecated
//                   implicit client, which has to be replaced before saving
export function downloadVia(value?: HttpClientRef): string {
  if (isZero(value)) return defaultHttpClientTag() ? 'default' : ''
  if (typeof value === 'string') return 'client:' + value
  const set = Object.entries(value as HttpClientOptions).filter(([, v]) => !isZero(v))
  const modelled = set.every(([key, v]) => key === 'detour' || (key === 'engine' && v === 'go'))
  if (!modelled) return 'custom'
  const detour = (value as HttpClientOptions).detour
  return detour ? 'out:' + detour : 'direct'
}

// The http_client to store for a select value; undefined means the key is left
// out. 'custom' keeps whatever the rule-set already carries.
export function downloadClient(via: string, current?: HttpClientRef): HttpClientRef | undefined {
  if (via === 'direct') return { ...DIRECT_HTTP_CLIENT }
  if (via.startsWith('client:')) return via.slice('client:'.length)
  if (via.startsWith('out:')) return { detour: via.slice('out:'.length) }
  if (via === 'custom') return current
  return undefined
}

// The select's choices. Labels come from the caller, which owns the i18n
// context; outbound and client tags are shown as they are.
export function downloadOptions(
  outTags: string[],
  labels: { defaultClient: string; direct: string; custom: string },
  current?: HttpClientRef,
): { value: string; title: string }[] {
  const options: { value: string; title: string }[] = []
  const defaultTag = defaultHttpClientTag()
  if (defaultTag) options.push({ value: 'default', title: labels.defaultClient + ' (' + defaultTag + ')' })
  options.push({ value: 'direct', title: labels.direct })
  for (const tag of httpClientTags()) options.push({ value: 'client:' + tag, title: tag + ' · HTTP' })
  for (const tag of outTags) {
    if (!isNoopDetour(tag)) options.push({ value: 'out:' + tag, title: tag })
  }
  if (downloadVia(current) === 'custom') options.push({ value: 'custom', title: labels.custom })
  return options
}

// What a new remote rule-set downloads with until the operator picks: the
// declared default when there is one, since following it is what naming no
// client means, and otherwise direct rather than any outbound in particular.
export function initialDownloadVia(): string {
  return defaultHttpClientTag() ? 'default' : 'direct'
}

// Rewrites a rule-set carrying the legacy download_detour, which sing-box 1.14
// deprecated and the panel no longer saves, into the http_client that does the
// same. A detour to an optionless direct outbound downloaded directly (the
// legacy field skipped the empty-direct check), so it becomes the direct
// client. A client already set wins, as it does in sing-box.
export function migrateDownloadDetour(ruleSet: any): void {
  if (!ruleSet || !('download_detour' in ruleSet)) return
  const detour = ruleSet.download_detour
  delete ruleSet.download_detour
  if (!isZero(ruleSet.http_client) || !detour) return
  ruleSet.http_client = isNoopDetour(detour) ? { ...DIRECT_HTTP_CLIENT } : { detour }
}
