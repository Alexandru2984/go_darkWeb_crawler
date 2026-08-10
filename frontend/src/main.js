import { createApp } from "vue";
import App from "./App.vue";
import { router } from "./router.js";
import { consumeLinkToken } from "./lib/linkTokens.js";
import "./style.css";

// Email links carry their credential in the URL fragment, which browsers never
// transmit to Cloudflare or nginx. Consume it before the router starts so the
// token is handed to the view in memory and the address bar is rewritten in the
// same tick — it must not survive into history or a bookmark.
consumeLinkToken();

createApp(App).use(router).mount("#app");
