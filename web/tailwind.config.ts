import type { Config } from 'tailwindcss'
import forms from '@tailwindcss/forms'
import typography from '@tailwindcss/typography'

// Tailwind reads the semantic token CSS variables defined in
// src/styles/tokens.css. Theme presets (Catppuccin Mocha/Latte, Tokyo
// Night, Solarized Light, custom) all feed the same variables, so
// switching theme is a single CSS class swap on <html>.
const tokenColor = (name: string) =>
  `rgb(var(--color-${name}) / <alpha-value>)`

export default {
  content: [
    './index.html',
    './src/**/*.{vue,ts,js}',
  ],
  darkMode: ['class', '[data-theme="dark"]'],
  theme: {
    extend: {
      colors: {
        bg: tokenColor('bg'),
        'bg-elevated': tokenColor('bg-elevated'),
        surface: tokenColor('surface'),
        'surface-hover': tokenColor('surface-hover'),
        text: tokenColor('text'),
        'text-muted': tokenColor('text-muted'),
        'text-inverse': tokenColor('text-inverse'),
        accent: tokenColor('accent'),
        'accent-hover': tokenColor('accent-hover'),
        'accent-fg': tokenColor('accent-fg'),
        danger: tokenColor('danger'),
        warning: tokenColor('warning'),
        success: tokenColor('success'),
        info: tokenColor('info'),
        border: tokenColor('border'),
        'border-strong': tokenColor('border-strong'),
        overlay: tokenColor('overlay'),
      },
      boxShadow: {
        sm: 'var(--shadow-sm)',
        DEFAULT: 'var(--shadow-md)',
        md: 'var(--shadow-md)',
        lg: 'var(--shadow-lg)',
      },
      borderRadius: {
        sm: 'var(--radius-sm)',
        DEFAULT: 'var(--radius-md)',
        md: 'var(--radius-md)',
        lg: 'var(--radius-lg)',
      },
      fontFamily: {
        sans: ['system-ui', '-apple-system', 'Segoe UI', 'Roboto', 'sans-serif'],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'monospace'],
      },
      // Note typography from the preset's tokens (IMP-154): `prose` follows
      // every theme, so no `prose-invert`, which painted light text on the
      // light presets.
      typography: {
        DEFAULT: {
          css: {
            '--tw-prose-body': 'rgb(var(--color-text))',
            '--tw-prose-headings': 'rgb(var(--color-text))',
            '--tw-prose-lead': 'rgb(var(--color-text-muted))',
            '--tw-prose-links': 'rgb(var(--color-text))',
            '--tw-prose-bold': 'rgb(var(--color-text))',
            '--tw-prose-counters': 'rgb(var(--color-text-muted))',
            '--tw-prose-bullets': 'rgb(var(--color-text-muted))',
            '--tw-prose-hr': 'rgb(var(--color-border))',
            '--tw-prose-quotes': 'rgb(var(--color-text))',
            '--tw-prose-quote-borders': 'rgb(var(--color-border-strong))',
            '--tw-prose-captions': 'rgb(var(--color-text-muted))',
            '--tw-prose-kbd': 'rgb(var(--color-text))',
            '--tw-prose-kbd-shadows': 'rgb(var(--color-border-strong) / 0.5)',
            '--tw-prose-code': 'rgb(var(--color-text))',
            '--tw-prose-pre-code': 'rgb(var(--color-text))',
            '--tw-prose-pre-bg': 'rgb(var(--color-bg-elevated))',
            '--tw-prose-th-borders': 'rgb(var(--color-border-strong))',
            '--tw-prose-td-borders': 'rgb(var(--color-border))',
          },
        },
      },
    },
  },
  plugins: [forms, typography],
} satisfies Config
