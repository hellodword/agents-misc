# i18n and Accessibility Checklist

## i18n

- [ ] Existing project locales are preserved; for greenfield user-facing UI, `en` copy exists.
- [ ] For greenfield user-facing UI, `zh-CN` copy exists.
- [ ] User-facing strings are localizable.
- [ ] Variables use interpolation.
- [ ] Dates, times, numbers, and plurals are locale-aware.
- [ ] Missing-key fallback is defined.
- [ ] No translated sentence fragments are concatenated unsafely.

## Accessibility

- [ ] Semantic HTML is used where possible.
- [ ] Form controls have labels.
- [ ] Focus states are visible.
- [ ] Keyboard navigation works.
- [ ] Color is not the only state signal.
- [ ] Error messages are actionable.
- [ ] Dialogs expose name, role, and state.
- [ ] Meaningful images have text alternatives.
