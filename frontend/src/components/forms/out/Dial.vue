<template>
  <div style="margin-bottom: 15px;">
    <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 12px;">
      <SectionLabel>{{ $t('objects.dial') }}</SectionLabel>
      <div class="grow" style="flex: 1;" />
      <Pop :min-width="230" direction="up">
        <template #trigger="{ toggle }">
          <Btn variant="subtle" sm @click="toggle">{{ $t('dial.options') }}</Btn>
        </template>
        <div style="display: flex; flex-direction: column; gap: 2px; padding: 4px;">
          <div v-if="mode != 'client' || detourTags?.length || dial.detour != undefined" class="pop-item"><SwitchLabel v-model="optionDetour" :label="$t('listen.detour')" /></div>
          <div v-if="mode != 'client'" class="pop-item"><SwitchLabel v-model="optionBind" :label="$t('dial.bindIf')" /></div>
          <div v-if="mode != 'client'" class="pop-item"><SwitchLabel v-model="optionIPV4" :label="$t('dial.bindIp4')" /></div>
          <div v-if="mode != 'client'" class="pop-item"><SwitchLabel v-model="optionIPV6" :label="$t('dial.bindIp6')" /></div>
          <div v-if="mode != 'client'" class="pop-item"><SwitchLabel v-model="optionBindNoPort" :label="$t('dial.bindNoPort')" /></div>
          <div v-if="mode != 'client'" class="pop-item"><SwitchLabel v-model="optionRM" label="Routing Mark" /></div>
          <div v-if="mode != 'client'" class="pop-item"><SwitchLabel v-model="optionRA" :label="$t('dial.reuseAddr')" /></div>
          <div class="pop-item"><SwitchLabel v-model="optionTCP" :label="$t('listen.tcpOptions')" /></div>
          <div class="pop-item"><SwitchLabel v-model="optionUDP" :label="$t('listen.udpOptions')" /></div>
          <div class="pop-item"><SwitchLabel v-model="optionCT" :label="$t('dial.connTimeout')" /></div>
          <div class="pop-item"><SwitchLabel v-model="optionTcpKeepAlive" :label="$t('dial.tcpKeepAlive')" /></div>
          <div v-if="mode != 'client'" class="pop-item"><SwitchLabel v-model="optionDR" :label="$t('dial.domainResolver')" /></div>
        </div>
      </Pop>
    </div>

    <div class="grid2">
      <Field v-if="optionDetour" :label="$t('dial.detourText')">
        <Select v-model="dial.detour">
          <option v-for="tag in detourOptions" :key="tag" :value="tag">{{ tag }}</option>
        </Select>
      </Field>
      <Field v-if="optionBind" :label="$t('dial.bindIf')">
        <input class="input mono" v-model="dial.bind_interface" />
      </Field>
      <Field v-if="optionIPV4" :label="$t('dial.bindIp4')">
        <input class="input mono" v-model="dial.inet4_bind_address" />
      </Field>
      <Field v-if="optionIPV6" :label="$t('dial.bindIp6')">
        <input class="input mono" v-model="dial.inet6_bind_address" />
      </Field>
      <Field v-if="optionRM" label="Linux Routing Mark">
        <input class="input mono" type="number" min="0" v-model.number="routingMark" />
      </Field>
      <Field v-if="optionCT" :label="$t('dial.connTimeout') + ' (' + $t('date.s') + ')'">
        <input class="input mono" type="number" min="1" v-model.number="connectTimeout" />
      </Field>
      <Field v-if="optionDR" :label="$t('dial.domainResolver')">
        <Select v-model="dial.domain_resolver">
          <option v-for="tag in dnsTags" :key="tag" :value="tag">{{ tag }}</option>
        </Select>
      </Field>
    </div>

    <div
      v-if="optionBindNoPort || optionRA || optionTCP || optionUDP"
      style="display: flex; gap: 20px; flex-wrap: wrap; margin-bottom: 14px;"
    >
      <SwitchLabel v-if="optionBindNoPort" v-model="bindNoPort" :label="$t('dial.bindNoPort')" />
      <SwitchLabel v-if="optionRA" v-model="reuseAddr" :label="$t('dial.reuseAddr')" />
      <SwitchLabel v-if="optionTCP" v-model="tcpFastOpen" label="TCP Fast Open" />
      <SwitchLabel v-if="optionTCP" v-model="tcpMultiPath" label="TCP Multi Path" />
      <SwitchLabel v-if="optionUDP" v-model="udpFragment" label="UDP Fragment" />
    </div>

    <template v-if="optionTcpKeepAlive">
      <div style="margin-bottom: 12px;">
        <SwitchLabel v-model="disableTcpKeepAlive" :label="$t('dial.disableTcpKeepAlive')" />
      </div>
      <div class="grid2">
        <Field :label="$t('dial.tcpKeepAlive')">
          <input class="input mono" v-model="dial.tcp_keep_alive" />
        </Field>
        <Field :label="$t('dial.tcpKeepAliveInterval')">
          <input class="input mono" v-model="dial.tcp_keep_alive_interval" />
        </Field>
      </div>
    </template>
  </div>
</template>

<script lang="ts" setup>
import Select from '@/components/ui/Select.vue'
import { computed } from 'vue'
import Data from '@/store/modules/data'
import Field from '@/components/ui/Field.vue'
import Btn from '@/components/ui/Btn.vue'
import Pop from '@/components/ui/Pop.vue'
import SwitchLabel from '@/components/ui/SwitchLabel.vue'
import SectionLabel from '@/components/ui/SectionLabel.vue'
import { optionField, valueField } from '@/plugins/formField'

// mode="client" hides the toggles that only make sense server-side; the fields
// themselves still render if the entity already carries those keys.
//
// detourTags replaces the outbounds as what a detour may name. A client config
// is not dialled from this panel's core, so its outbounds mean nothing there:
// the inbound drawer passes the inbounds whose client configs it can ride,
// and the subscription rewrites the tag to theirs (#255). In client mode the
// detour toggle shows only when there is one to pick, or one to take back.
const props = defineProps<{ dial: any; mode?: string; detourTags?: string[] }>()

const d = () => props.dial

const outTags = computed(() => props.detourTags ?? Data().outboundTags)
// A detour whose target was deleted or renamed stays listed, so the field
// shows what is stored rather than a blank that reads as unset.
const detourOptions = computed((): string[] => {
  const current = props.dial.detour
  return current && !outTags.value.includes(current) ? [current, ...outTags.value] : outTags.value
})
const dnsTags = computed(() => Data().config.dns?.servers?.map((d: any) => d.tag) ?? [])

const connectTimeout = computed({
  get: () => props.dial.connect_timeout ? parseInt(props.dial.connect_timeout.replace('s', '')) : 5,
  set: (v: number) => { props.dial.connect_timeout = v > 0 ? v + 's' : '5s' },
})
const routingMark = computed({
  get: () => props.dial.routing_mark ?? 0,
  set: (v: number) => { props.dial.routing_mark = v > 0 ? v : 0 },
})
const bindNoPort = valueField(d, 'bind_address_no_port', false)
const reuseAddr = valueField(d, 'reuse_addr', false)
const tcpFastOpen = valueField(d, 'tcp_fast_open', false)
const tcpMultiPath = valueField(d, 'tcp_multi_path', false)
const udpFragment = valueField(d, 'udp_fragment', false)
const disableTcpKeepAlive = valueField(d, 'disable_tcp_keep_alive', false)

const optionDetour = computed({
  get: (): boolean => props.dial.detour != undefined,
  set: (v: boolean) => { v ? props.dial.detour = outTags.value[0] ?? '' : delete props.dial.detour },
})
const optionBind = optionField(d, 'bind_interface', () => '', 'delete')
const optionIPV4 = optionField(d, 'inet4_bind_address', () => '', 'delete')
const optionIPV6 = optionField(d, 'inet6_bind_address', () => '', 'delete')
const optionBindNoPort = optionField(d, 'bind_address_no_port', () => true, 'delete')
const optionTcpKeepAlive = computed({
  get: (): boolean =>
    props.dial.disable_tcp_keep_alive != undefined ||
    props.dial.tcp_keep_alive != undefined ||
    props.dial.tcp_keep_alive_interval != undefined,
  set: (v: boolean) => {
    if (v) {
      props.dial.tcp_keep_alive = '5m'
      props.dial.tcp_keep_alive_interval = '75s'
    } else {
      delete props.dial.disable_tcp_keep_alive
      delete props.dial.tcp_keep_alive
      delete props.dial.tcp_keep_alive_interval
    }
  },
})
const optionRM = optionField(d, 'routing_mark', () => 0, 'delete')
const optionRA = optionField(d, 'reuse_addr', () => true, 'delete')
const optionTCP = computed({
  get: (): boolean =>
    props.dial.tcp_fast_open != undefined &&
    props.dial.tcp_multi_path != undefined,
  set: (v: boolean) => {
    if (v) {
      props.dial.tcp_fast_open = false
      props.dial.tcp_multi_path = false
    } else {
      delete props.dial.tcp_fast_open
      delete props.dial.tcp_multi_path
    }
  },
})
const optionUDP = optionField(d, 'udp_fragment', () => true, 'delete')
// '5s' not 5 — connectTimeout's getter does .replace('s', '') on it
const optionCT = optionField(d, 'connect_timeout', () => '5s', 'delete')
const optionDR = computed({
  get: (): boolean => props.dial.domain_resolver != undefined,
  set: (v: boolean) => { props.dial.domain_resolver = v ? dnsTags.value[0] ?? '' : undefined },
})
</script>
