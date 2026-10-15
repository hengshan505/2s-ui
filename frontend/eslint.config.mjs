import js from '@eslint/js'
import pluginVue from 'eslint-plugin-vue'
import tseslint from 'typescript-eslint'
import globals from 'globals'

export default tseslint.config(
  {
    // 取代旧 --ignore-path .gitignore
    ignores: ['dist/**', 'subscriber/dist/**', 'node_modules/**'],
  },

  js.configs.recommended,
  ...pluginVue.configs['flat/essential'],

  // .ts / .mts 用 TS 解析器
  {
    files: ['**/*.{ts,mts,tsx}'],
    languageOptions: { parser: tseslint.parser },
  },
  // .vue 的 <script lang="ts"> 交给 TS 解析器(外层仍是 vue-eslint-parser)
  {
    files: ['**/*.vue'],
    languageOptions: { parserOptions: { parser: tseslint.parser } },
  },

  // TS 编译器已覆盖的核心规则关掉(no-undef 等),.vue 一并适用,
  // 否则 defineProps/defineEmits 等编译宏会报 no-undef
  {
    files: ['**/*.{ts,mts,tsx,vue}'],
    rules: { ...tseslint.configs.eslintRecommended.rules },
  },

  {
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
    },
    rules: {
      'vue/multi-word-component-names': 'off',
      // 表单组件的既有模式:父组件传对象,子组件直接改字段(v-model 绑 prop 对象属性),
      // 只禁止整个 prop 被重新赋值
      'vue/no-mutating-props': ['error', { shallowOnly: true }],
      // Select.vue 等处有意使用 == 宽松比较
      eqeqeq: 'off',
      // 存量代码对齐:以下规则在旧 .eslintrc 时代未启用,存量违例集中在
      // forms/drawers/plugins 等老文件;降为 warn 保留可见性、不挡发布,
      // 待专项清理后再升回 error
      'no-unsafe-optional-chaining': 'warn',
      'no-prototype-builtins': 'warn',
      'no-unused-vars': 'warn',
      'no-useless-assignment': 'warn',
    },
  },
)
