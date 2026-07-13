import React from "react"
import ReactDOM from "react-dom/client"
import { BrowserRouter } from "react-router-dom"
import "@/lib/i18n"
import "@/styles.css"
import { App } from "@/App"

const storedTheme = localStorage.getItem("agents-viewer-theme") ?? "system"
const dark = storedTheme === "dark" || (storedTheme === "system" && matchMedia("(prefers-color-scheme: dark)").matches)
document.documentElement.classList.toggle("dark", dark)

ReactDOM.createRoot(document.getElementById("root")!).render(<React.StrictMode><BrowserRouter><App /></BrowserRouter></React.StrictMode>)
