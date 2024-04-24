const wform = document.getElementById("write")
const lform = document.getElementById("list")
const gatewayInput = document.querySelector("input[name=\"gateway\"]")
gatewayInput.value = window.location.origin

wform.onsubmit = async e => {
    e.preventDefault()
    const button = wform.querySelector("button[type=\"submit\"]")
    button.setAttribute("disabled","")
    const gateway = gatewayInput.value
    const output = wform.querySelector("input[name=\"work\"]")
    const val = wform.querySelector("input[name=\"val\"]").value
    const encoder = new TextEncoder()
    const valBytes = encoder.encode(val)
    const difficulty = wform.querySelector("input[name=\"difficulty\"]").value
    const {workhash,nonce} = await work(valBytes, difficulty, button)
    output.value = bytesToHex(workhash)
    const body = { val, nonce: bytesToHex(nonce), work: bytesToHex(workhash) }
    button.textContent = "Sending..."
    const resp = await fetch(gateway, { method: "POST", body: JSON.stringify(body)})
    button.textContent = "Send"
    button.removeAttribute("disabled")
    if (resp.status !== 200) {
        output.value = await resp.text()
        return
    }
    const link = wform.querySelector("a#work")
    link.href = `/${bytesToHex(workhash)}`
    link.textContent = bytesToHex(workhash)
}

lform.onsubmit = async e => {
    e.preventDefault()
    const button = lform.querySelector("button[type=\"submit\"]")
    button.setAttribute("disabled","")
    const gateway = gatewayInput.value
    const prefix = lform.querySelector("input[name=\"prefix\"]").value
    let dats = await fetch(`${gateway}/list/${prefix}`)
    const results = lform.querySelector("#results")
    while (results.firstChild) {
        results.removeChild(results.firstChild)
    }
    button.removeAttribute("disabled")
    if (!dats.ok) {
        r = document.createElement("div")
        r.textContent = `Error: ${await dats.text()}`
        results.appendChild(r)
        return
    }
    for (let dat of (await dats.json()).sort((a,b) => b.added - a.added)) {
        r = document.createElement("div")
        r.textContent = dat.val
        results.appendChild(r)
    }
}

async function work(valBytes, difficulty, button) {
    const hash = await crypto.subtle.digest("SHA-256", valBytes)
    const load = new Uint8Array(hash)
    const nonce = new Uint8Array(32)
    const nonceBytes = new Uint8Array(32)
    const input = new Uint8Array(load.length + 32)
    input.set(load)
    let i = 0
    while (true) {
        if (++i % 1000 == 0) {
            button.textContent = i
        }
        crypto.getRandomValues(nonce)
        nonceBytes.set(nonce)
        input.set(nonce, load.length)
        const hashBuffer = await crypto.subtle.digest("SHA-256", input)
        const workhash = new Uint8Array(hashBuffer)
        if (isDone(workhash, difficulty)) {
            return {workhash, nonce}
        } 
    }
}

function isDone(work, difficulty) {
    for (let i = 0; i < difficulty; i++) {
        if (work[i] !== 0) {
            return false
        }
    }
    return true
}

function bytesToHex(bytes) {
    const hex = new Array(bytes.length * 2);
    for (let i = 0; i < bytes.length; i++) {
        const value = bytes[i];
        hex[i * 2] = value >>> 4;
        hex[i * 2 + 1] = value & 0x0F;
    }
    return hex.map(x => x.toString(16)).join("");
}
