<template>
  <!-- 延迟测试用的 URL:关着就交给 sing-box 默认的 generate_204;只存本浏览器,
       出站和端点两个页面共用一份(见 useDelayCheck) -->
  <Pop :width="300">
    <template #trigger="{ toggle }">
      <Btn sm :title="testLink ?? $t('out.testUrlDefault')" :class="{ active: !!testLink }" @click="toggle">
        <Ico name="link" :size="15" /> {{ $t('out.testUrl') }}
      </Btn>
    </template>
    <div class="body">
      <SwitchLabel v-model="testUrlEnabled" :label="$t('out.testUrlCustom')" />
      <input
        v-model.trim="testUrl"
        class="input mono"
        dir="ltr"
        spellcheck="false"
        placeholder="https://www.gstatic.com/generate_204"
        :disabled="!testUrlEnabled"
      />
      <div v-if="invalid" class="note" style="color: var(--rose);">{{ $t('out.testUrlInvalid') }}</div>
      <div v-else class="note">{{ $t('out.testUrlHint') }}</div>
    </div>
  </Pop>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { isValidTestUrl, useTestUrl } from '@/plugins/useDelayCheck'
import Pop from '@/components/ui/Pop.vue'
import Btn from '@/components/ui/Btn.vue'
import Ico from '@/components/ui/Ico.vue'
import SwitchLabel from '@/components/ui/SwitchLabel.vue'

const { testUrlEnabled, testUrl, testLink } = useTestUrl()

const invalid = computed(() => testUrlEnabled.value && testUrl.value !== '' && !isValidTestUrl(testUrl.value))
</script>

<style scoped>
.body { display: flex; flex-direction: column; gap: 10px; padding: 10px; }
.note { font-size: 11.5px; color: var(--text-3); }
.active { color: var(--brand); }
</style>
