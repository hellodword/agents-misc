import { test, expect } from "./fixtures"

const password = "correct horse:电池订书钉"

test.use({ password })

test("requires Basic credentials before serving the viewer, API, or event stream", async ({
  browser,
  baseURL,
  page,
}) => {
  const unauthenticated = await browser.newContext({ baseURL })
  const wrongCredentials = await browser.newContext({
    baseURL,
    httpCredentials: { username: "agents-viewer", password: "wrong" },
  })
  try {
    for (const path of ["/", "/api/v1/status", "/api/v1/events"]) {
      const response = await unauthenticated.request.get(path)
      expect(response.status(), path).toBe(401)
      expect(response.headers()["www-authenticate"]).toBe(
        'Basic realm="agents-viewer", charset="UTF-8"',
      )
    }
    expect(
      (await wrongCredentials.request.get("/api/v1/status")).status(),
    ).toBe(401)
  } finally {
    await wrongCredentials.close()
    await unauthenticated.close()
  }

  await expect(page.getByText("Pagination message 109").first()).toBeVisible()
  expect((await page.request.get("/api/v1/status")).ok()).toBeTruthy()
  expect(
    await page.evaluate(async () => {
      const response = await fetch("/api/v1/events")
      await response.body?.cancel()
      return response.status
    }),
  ).toBe(200)
})
