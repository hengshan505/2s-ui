<template>
  <MDrawer
    :open="open"
    icon="rules"
    :color="form.type === 'remote' ? 'var(--violet)' : 'var(--cyan)'"
    :title="isNew ? $t('ui.rsNew') : form.tag"
    :sub="$t('ui.rsSub')"
    :save-label="isNew ? $t('ui.create') : $t('actions.save')"
    :width="500"
    @close="$emit('close')"
    @save="saveChanges"
  >
    <Field :label="$t('objects.tag')">
      <input class="input mono" v-model="form.tag" placeholder="geosite-ads" />
    </Field>

    <!-- local / remote -->
    <Segmented v-model="kind" block :options="[['local', $t('ruleset.local')], ['remote', $t('ruleset.remote')]]" />

    <Field :label="$t('ruleset.format')">
      <Select v-model="form.format">
        <option value="source">source</option>
        <option value="binary">binary</option>
      </Select>
    </Field>

    <template v-if="form.type === 'local'">
      <Field :label="$t('transport.path')">
        <input class="input mono" v-model="form.path" placeholder="/etc/sing-box/rules.srs" />
      </Field>
    </template>
    <template v-else>
      <Field label="URL">
        <input class="input mono" style="font-size: 12px;" v-model="form.url" placeholder="https://…/geosite-ads.srs" />
      </Field>
      <!-- Read once at start when nothing is cached, so the first download does
           not hold the core up. The set still refreshes in the background. -->
      <Field :label="$t('ruleset.initialPath')">
        <input class="input mono" style="font-size: 12px;" v-model="initialPath" placeholder="/etc/sing-box/geosite-ads.srs" />
      </Field>
      <!-- One choice, always made: a shared client, an outbound, direct, or
           the config's default client when it declares one. Naming none with
           no default falls back to the implicit client sing-box 1.14
           deprecated, so that is not on offer (see plugins/httpClient.ts). -->
      <Field :label="$t('ruleset.download')">
        <Select v-model="via">
          <option v-if="via === ''" value="" disabled>{{ $t('ui.selectHint') }}</option>
          <option v-for="o in viaOptions" :key="o.value" :value="o.value">{{ o.title }}</option>
        </Select>
      </Field>
      <Field :label="$t('ruleset.interval') + ' (' + $t('date.d') + ')'">
        <input class="input mono" type="number" min="0" v-model.number="updateIntervals" />
      </Field>
    </template>
  </MDrawer>
</template>

<script lang="ts" setup>
import Select from '@/components/ui/Select.vue'
import { computed, ref, watch } from 'vue'
import MDrawer from '@/components/ui/MDrawer.vue'
import Segmented from '@/components/ui/Segmented.vue'
import Field from '@/components/ui/Field.vue'
import RandomUtil from '@/plugins/randomUtil'
import { ruleset } from '@/types/rules'
import { useI18n } from 'vue-i18n'
import { downloadClient, downloadOptions, downloadVia, initialDownloadVia, migrateDownloadDetour } from '@/plugins/httpClient'

const props = defineProps<{
  open: boolean
  index: number
  data: string
  outTags: string[]
}>()
const emit = defineEmits<{ close: []; save: [data: ruleset] }>()

const isNew = computed(() => props.index === -1)

const form = ref<ruleset>(<ruleset>{})

function init() {
  if (props.index !== -1) {
    form.value = <ruleset>JSON.parse(props.data)
    migrateDownloadDetour(form.value)
  } else {
    form.value = <ruleset>{ type: 'local', tag: 'rs-' + RandomUtil.randomSeq(3), format: 'binary' }
  }
}
watch(() => props.open, (v) => { if (v) init() })

// switching type clears the fields of the other kind (legacy updateType)
const kind = computed({
  get: () => form.value.type,
  set: (type) => {
    const was = form.value.type
    form.value.type = <'local' | 'remote'>type
    if (type === 'local') {
      delete form.value.url
      delete form.value.http_client
      delete form.value.update_interval
      delete form.value.initial_path
    } else {
      delete form.value.path
      // A remote set has to say how it downloads, so it starts out saying so
      // -- once, not again when Remote is picked while already selected.
      if (was !== 'remote') via.value = initialDownloadVia()
    }
  },
})

const { t } = useI18n()

// Built from the stored client each time rather than remembered: every choice
// but 'custom' is fully described by the client it writes.
const via = computed({
  get: () => downloadVia(form.value.http_client),
  set: (v: string) => {
    const client = downloadClient(v, form.value.http_client)
    if (client !== undefined) form.value.http_client = client
    else delete form.value.http_client
  },
})

const viaOptions = computed(() =>
  downloadOptions(
    props.outTags,
    { defaultClient: t('ruleset.defaultClient'), direct: t('ruleset.direct'), custom: t('ruleset.customClient') },
    form.value.http_client,
  ),
)

// saveChanges emits the form as it stands, so an emptied field has to leave no
// key rather than an empty string sing-box would read as a path.
const initialPath = computed({
  get: (): string => form.value.initial_path ?? '',
  set: (v: string) => {
    const trimmed = (v ?? '').trim()
    if (trimmed) form.value.initial_path = trimmed
    else delete form.value.initial_path
  },
})

const updateIntervals = computed({
  get: () => (form.value.update_interval !== undefined ? parseInt(form.value.update_interval.replace('d', '')) : 0),
  set: (v: number) => { form.value.update_interval = v > 0 ? v + 'd' : undefined },
})

function saveChanges() {
  emit('save', form.value)
}
</script>
