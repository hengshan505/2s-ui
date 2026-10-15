<template>
  <Modal
    :open="open"
    :title="mode === 'rules' ? $t('rule.import.rulesTitle') : $t('rule.import.title')"
    :width="760"
    @close="$emit('close')"
  >
    <div style="padding: 16px 18px;">
      <!-- ===================== rules (JSON route block) ===================== -->
      <template v-if="mode === 'rules'">
        <Tabs v-model="tab" :tabs="[['json', 'JSON'], ['file', $t('rule.import.uploadFile')], ['url', $t('rule.import.fromUrl')]]" :mb="14" />

        <template v-if="tab === 'json'">
          <MHint style="margin-bottom: 12px;">{{ $t('rule.import.jsonHint') }}</MHint>
          <textarea
            class="input mono"
            rows="12"
            spellcheck="false"
            style="height: auto; padding: 10px 12px; font-size: 12.5px;"
            v-model="rawJson"
          ></textarea>
        </template>

        <template v-if="tab === 'file'">
          <MHint style="margin-bottom: 12px;">{{ $t('rule.import.fileJsonHint') }}</MHint>
          <input ref="jsonFileEl" type="file" accept=".json" style="display: none;" @change="onRulesFile" />
          <Btn @click="jsonFileEl?.click()">
            <Ico name="upload" :size="15" /> {{ $t('rule.import.selectJson') }}
          </Btn>
          <span v-if="fileName" class="mono" style="margin-inline-start: 10px; font-size: 12.5px; color: var(--text-3);">{{ fileName }}</span>
        </template>

        <template v-if="tab === 'url'">
          <MHint style="margin-bottom: 12px;">{{ $t('rule.import.urlHint') }}</MHint>
          <div style="display: flex; gap: 8px;">
            <input
              class="input mono"
              style="flex: 1;"
              v-model="fetchUrl"
              spellcheck="false"
              placeholder="https://raw.githubusercontent.com/.../rules.json"
              @keydown.enter="fetchFromUrl"
            />
            <Btn variant="subtle" icon :loading="fetching" :title="$t('rule.import.parse')" @click="fetchFromUrl">
              <Ico name="download" :size="16" />
            </Btn>
          </div>
        </template>

        <div v-if="error" style="margin-top: 12px; font-size: 12.5px; font-weight: 600; color: var(--rose);">{{ error }}</div>

        <template v-if="parsed">
          <hr class="form-divider" style="margin: 16px 0;" />

          <!-- conflict resolution -->
          <div v-if="hasConflicts" style="margin-bottom: 14px;">
            <div style="font-size: 13px; font-weight: 700; color: var(--amber); margin-bottom: 4px;">{{ $t('rule.import.conflict') }}</div>
            <div style="font-size: 12.5px; color: var(--text-3); margin-bottom: 8px;">
              {{ $t('rule.import.conflictDesc', { rules: existingRulesCount, rulesets: existingRulesetsCount }) }}
            </div>
            <div style="display: flex; flex-direction: column; gap: 2px;">
              <!-- Check 仅作装饰:button 内禁止嵌套可聚焦控件 -->
              <button type="button" class="pop-item" :class="{ active: importMode === 'merge' }" @click="importMode = 'merge'">
                <Check :checked="importMode === 'merge'" tabindex="-1" aria-hidden="true" /> {{ $t('rule.import.merge') }}
              </button>
              <button type="button" class="pop-item" :class="{ active: importMode === 'replace' }" @click="importMode = 'replace'">
                <Check :checked="importMode === 'replace'" tabindex="-1" aria-hidden="true" /> {{ $t('rule.import.replace') }}
              </button>
            </div>
          </div>

          <!-- final outbound -->
          <div v-if="parsed.final" style="display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 14px;">
            <span style="font-size: 12.5px; color: var(--text-3);">{{ $t('rule.import.finalOutbound') }}:</span>
            <Chip color="brand">{{ parsed.final }}</Chip>
            <CheckLabel :label="$t('rule.import.applyFinal')" v-model="applyFinal" />
          </div>

          <!-- rules preview -->
          <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px;">
            <SectionLabel>{{ $t('pages.rules') }}</SectionLabel>
            <Chip v-if="(parsed.rules?.length ?? 0) > 0" color="emerald">{{ parsed.rules.length }}</Chip>
          </div>
          <table v-if="parsed.rules?.length" class="dtable" style="margin-bottom: 16px;">
            <thead>
              <tr><th>#</th><th>{{ $t('type') }}</th><th>{{ $t('admin.action') }}</th><th>{{ $t('objects.outbound') }}</th></tr>
            </thead>
            <tbody>
              <tr v-for="(r, i) in parsed.rules" :key="i">
                <td class="mono" style="padding: 8px 16px;">{{ Number(i) + 1 }}</td>
                <td style="padding: 8px 16px;">{{ r.type ?? 'simple' }}</td>
                <td style="padding: 8px 16px;">{{ r.action }}</td>
                <td class="mono" style="padding: 8px 16px;">{{ r.outbound ?? '—' }}</td>
              </tr>
            </tbody>
          </table>

          <!-- rulesets preview -->
          <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px;">
            <SectionLabel>{{ $t('rule.ruleset') }}</SectionLabel>
            <Chip v-if="(parsed.rule_set?.length ?? 0) > 0" color="emerald">{{ parsed.rule_set.length }}</Chip>
            <Chip v-if="skippedRulesets > 0" color="amber" :title="$t('rule.import.skipped')">{{ skippedRulesets }}</Chip>
          </div>
          <table v-if="parsed.rule_set?.length" class="dtable">
            <thead>
              <tr><th>{{ $t('objects.tag') }}</th><th>{{ $t('ruleset.format') }}</th><th>{{ $t('type') }}</th><th>{{ $t('ruleset.interval') }}</th></tr>
            </thead>
            <tbody>
              <tr
                v-for="(rs, i) in parsed.rule_set"
                :key="i"
                :style="importMode === 'merge' && existingRulesetTags.includes(rs.tag) ? { opacity: 0.4 } : undefined"
              >
                <td class="mono" style="padding: 8px 16px;">{{ rs.tag }}</td>
                <td style="padding: 8px 16px;">{{ rs.format }}</td>
                <td style="padding: 8px 16px;">{{ rs.type }}</td>
                <td class="mono" style="padding: 8px 16px;">{{ rs.update_interval ?? '—' }}</td>
              </tr>
            </tbody>
          </table>
        </template>
      </template>

      <!-- ===================== rulesets (URL list) ===================== -->
      <template v-else>
        <Tabs v-model="rsTab" :tabs="[['text', $t('rule.import.pasteUrls')], ['file', $t('rule.import.uploadTxt')]]" :mb="14" />

        <template v-if="rsTab === 'text'">
          <MHint style="margin-bottom: 12px;">{{ $t('rule.import.urlsHint') }}</MHint>
          <textarea
            class="input mono"
            rows="10"
            spellcheck="false"
            style="height: auto; padding: 10px 12px; font-size: 12.5px;"
            placeholder="https://github.com/.../geoip-telegram.srs&#10;https://github.com/.../geosite-youtube.srs"
            v-model="importRawText"
          ></textarea>
        </template>

        <template v-if="rsTab === 'file'">
          <MHint style="margin-bottom: 12px;">{{ $t('rule.import.fileHint') }}</MHint>
          <input ref="txtFileEl" type="file" accept=".txt" style="display: none;" @change="onRulesetsFile" />
          <Btn @click="txtFileEl?.click()">
            <Ico name="upload" :size="15" /> {{ $t('rule.import.selectTxt') }}
          </Btn>
          <span v-if="fileName" class="mono" style="margin-inline-start: 10px; font-size: 12.5px; color: var(--text-3);">{{ fileName }}</span>
        </template>

        <div class="field-grid" style="margin-top: 16px;">
          <Field :label="$t('ruleset.format')" :mb="0">
            <Select v-model="importFormat">
              <option value="source">source</option>
              <option value="binary">binary</option>
            </Select>
          </Field>
          <Field :label="$t('ruleset.download')" :mb="0">
            <Select v-model="importVia">
              <option v-for="o in importViaOptions" :key="o.value" :value="o.value">{{ o.title }}</option>
            </Select>
          </Field>
          <Field :label="$t('ruleset.interval') + ' (' + $t('date.d') + ')'" :mb="0">
            <input class="input mono" type="number" min="0" v-model.number="importInterval" />
          </Field>
        </div>

        <template v-if="importPreview.length > 0">
          <hr class="form-divider" style="margin: 16px 0;" />
          <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px;">
            <SectionLabel>{{ $t('rule.import.preview') }}</SectionLabel>
            <Chip color="emerald">{{ importPreview.length }}</Chip>
            <Chip v-if="importSkipped > 0" color="amber" :title="$t('rule.import.skipped')">{{ importSkipped }}</Chip>
          </div>
          <table class="dtable">
            <thead>
              <tr><th>{{ $t('objects.tag') }}</th><th>{{ $t('ruleset.format') }}</th><th>URL</th><th></th></tr>
            </thead>
            <tbody>
              <tr v-for="(item, i) in importPreview" :key="i" :style="item.exists ? { opacity: 0.4 } : undefined">
                <td class="mono" style="padding: 4px 16px;">{{ item.tag }}</td>
                <td style="padding: 4px 16px;">{{ item.format }}</td>
                <td class="mono" dir="ltr" style="padding: 4px 16px;" :title="item.url">…/{{ item.url.split('/').pop() ?? item.url }}</td>
                <td style="padding: 4px 16px;"><IconBtn name="trash" danger :title="$t('actions.del')" @click="importPreview.splice(i, 1)" /></td>
              </tr>
            </tbody>
          </table>
        </template>
      </template>
    </div>

    <template #footer>
      <template v-if="mode === 'rules'">
        <Btn v-if="tab === 'json'" variant="subtle" :disabled="rawJson.trim().length === 0" @click="parseJson">
          <Ico name="search" :size="15" /> {{ $t('rule.import.parse') }}
        </Btn>
        <div style="flex: 1;" />
        <Btn @click="$emit('close')">{{ $t('actions.close') }}</Btn>
        <Btn variant="primary" :disabled="!parsed" @click="saveRules">
          <Ico name="check" :size="15" /> {{ $t('actions.save') }}
        </Btn>
      </template>
      <template v-else>
        <Btn variant="subtle" :disabled="importRawText.trim().length === 0" @click="parseImport">
          <Ico name="search" :size="15" /> {{ $t('rule.import.parse') }}
        </Btn>
        <div style="flex: 1;" />
        <Btn @click="$emit('close')">{{ $t('actions.close') }}</Btn>
        <Btn variant="primary" :disabled="newCount === 0" @click="saveRulesets">
          <Ico name="check" :size="15" /> {{ $t('actions.save') }}
        </Btn>
      </template>
    </template>
  </Modal>
</template>

<script lang="ts" setup>
import Select from '@/components/ui/Select.vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Modal from '@/components/ui/Modal.vue'
import Tabs from '@/components/ui/Tabs.vue'
import Btn from '@/components/ui/Btn.vue'
import Ico from '@/components/ui/Ico.vue'
import Chip from '@/components/ui/Chip.vue'
import Check from '@/components/ui/Check.vue'
import CheckLabel from '@/components/ui/CheckLabel.vue'
import Field from '@/components/ui/Field.vue'
import IconBtn from '@/components/ui/IconBtn.vue'
import MHint from '@/components/ui/MHint.vue'
import SectionLabel from '@/components/ui/SectionLabel.vue'
import { downloadClient, downloadOptions, initialDownloadVia, migrateDownloadDetour } from '@/plugins/httpClient'

interface ImportItem { tag: string; url: string; format: string; exists: boolean }

const props = defineProps<{
  open: boolean
  mode: 'rules' | 'rulesets'
  existingRulesCount: number
  existingRulesetsCount: number
  existingRulesetTags: string[]
  outTags: string[]
  rsTags: string[]
}>()
const emit = defineEmits<{
  close: []
  saveRules: [block: any, mode: 'merge' | 'replace', applyFinal: boolean]
  saveRulesets: [items: any[]]
}>()

const { t } = useI18n()

const jsonFileEl = ref<HTMLInputElement | null>(null)
const txtFileEl = ref<HTMLInputElement | null>(null)
const fileName = ref('')

// ---- rules (JSON) import state ----
const tab = ref('json')
const rawJson = ref('')
const fetchUrl = ref('')
const fetching = ref(false)
const error = ref('')
const parsed = ref<any>(null)
const importMode = ref<'merge' | 'replace'>('merge')
const applyFinal = ref(false)

const hasConflicts = computed(() => props.existingRulesCount > 0 || props.existingRulesetsCount > 0)
const skippedRulesets = computed(() => {
  if (!parsed.value?.rule_set) return 0
  const existing = new Set(props.existingRulesetTags)
  return parsed.value.rule_set.filter((rs: any) => existing.has(rs.tag)).length
})

function tabChanged() {
  rawJson.value = ''
  fetchUrl.value = ''
  error.value = ''
  parsed.value = null
  fileName.value = ''
  importMode.value = hasConflicts.value ? 'merge' : 'replace'
  applyFinal.value = false
}
watch(tab, tabChanged)

function extractRouteBlock(obj: any): any {
  if (obj?.route && (obj.route.rules || obj.route.rule_set)) return obj.route
  if (obj?.rules || obj?.rule_set) return obj
  return null
}

function setParsed(block: any) {
  // A route block copied from elsewhere may still carry the legacy field,
  // which the panel no longer saves.
  if (Array.isArray(block.rule_set)) block.rule_set.forEach(migrateDownloadDetour)
  parsed.value = block
  importMode.value = hasConflicts.value ? 'merge' : 'replace'
  applyFinal.value = false
}

function parseJson() {
  error.value = ''
  parsed.value = null
  try {
    const block = extractRouteBlock(JSON.parse(rawJson.value))
    if (!block) {
      error.value = t('rule.import.errNoArrays')
      return
    }
    setParsed(block)
  } catch (e: any) {
    error.value = t('rule.import.errJsonParse', { message: e.message })
  }
}

async function fetchFromUrl() {
  error.value = ''
  parsed.value = null
  fetching.value = true
  try {
    const resp = await fetch(fetchUrl.value)
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
    const block = extractRouteBlock(await resp.json())
    if (!block) error.value = t('rule.import.errNoArraysFetched')
    else setParsed(block)
  } catch (e: any) {
    error.value = t('rule.import.errFetch', { message: e.message })
  } finally {
    fetching.value = false
  }
}

async function onRulesFile(event: Event) {
  error.value = ''
  parsed.value = null
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) {
    error.value = t('rule.import.errNoFile')
    return
  }
  fileName.value = file.name
  try {
    const block = extractRouteBlock(JSON.parse(await file.text()))
    if (!block) {
      error.value = t('rule.import.errNoArraysInFile')
      return
    }
    setParsed(block)
  } catch (e: any) {
    error.value = t('rule.import.errJsonParse', { message: e.message })
  }
}

function saveRules() {
  if (!parsed.value) return
  emit('saveRules', parsed.value, importMode.value, applyFinal.value)
}

// ---- rulesets (URL list) import state ----
const rsTab = ref('text')
const importRawText = ref('')
const importFormat = ref('binary')
const importVia = ref(initialDownloadVia())
const importInterval = ref(1)
const importPreview = ref<ImportItem[]>([])

const importViaOptions = computed(() =>
  downloadOptions(props.outTags, {
    defaultClient: t('ruleset.defaultClient'),
    direct: t('ruleset.direct'),
    custom: t('ruleset.customClient'),
  }),
)

const importSkipped = computed(() => importPreview.value.filter((i) => i.exists).length)
const newCount = computed(() => importPreview.value.filter((i) => !i.exists).length)

function rsTabChanged() {
  importPreview.value = []
  importRawText.value = ''
  fileName.value = ''
}
watch(rsTab, rsTabChanged)

function urlToTag(url: string): string {
  try {
    const filename = new URL(url).pathname.split('/').pop() ?? ''
    return filename.replace(/\.[^.]+$/, '')
  } catch {
    const parts = url.split('/')
    return parts[parts.length - 1].replace(/\.[^.]+$/, '') || url
  }
}

function parseImport() {
  const existingTags = new Set(props.rsTags)
  const seen = new Set<string>()
  importPreview.value = importRawText.value
    .split('\n').map((l) => l.trim()).filter((l) => l.length > 0 && l.startsWith('http'))
    .filter((url) => { if (seen.has(url)) return false; seen.add(url); return true })
    .map((url) => ({ tag: urlToTag(url), url, format: importFormat.value, exists: existingTags.has(urlToTag(url)) }))
}

async function onRulesetsFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  fileName.value = file.name
  importRawText.value = await file.text()
  parseImport()
}

function saveRulesets() {
  const toAdd = importPreview.value.filter((i) => !i.exists).map((item) => {
    const rs: any = { type: 'remote', tag: item.tag, format: item.format, url: item.url }
    const client = downloadClient(importVia.value)
    if (client !== undefined) rs.http_client = client
    if (importInterval.value > 0) rs.update_interval = importInterval.value + 'd'
    return rs
  })
  emit('saveRulesets', toAdd)
}

// ---- reset on open ----
watch(() => props.open, (v) => {
  if (v) {
    tab.value = 'json'
    rsTab.value = 'text'
    importFormat.value = 'binary'
    importVia.value = initialDownloadVia()
    importInterval.value = 1
    tabChanged()
    rsTabChanged()
  }
})
</script>
