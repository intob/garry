const form = document.querySelector("form")
const button = form.querySelector("button[type=\"submit\"]")
const gatewayInput = form.querySelector("input[name=\"gateway\"]")
const output = form.querySelector("input[name=\"work\"]")
const hexout = form.querySelector("input[name=\"hexout\"]")
gatewayInput.value = window.location.origin
form.onsubmit = async e => {
    e.preventDefault()
    button.setAttribute("disabled","")
    const gateway = gatewayInput.value
    const val = form.querySelector("input[name=\"val\"]").value
    const encoder = new TextEncoder()
    const valBytes = encoder.encode(val)
    hexout.value = bytesToHex(valBytes)
    const difficulty = form.querySelector("input[name=\"difficulty\"]").value
    const {workhash,nonce} = await work(valBytes, difficulty, button)
    output.value = bytesToHex(workhash)
    const body = { val, noncehex: bytesToHex(nonce), workhex: bytesToHex(workhash) }
    button.textContent = "Sending..."
    const resp = await fetch(gateway, { method: "POST", body: JSON.stringify(body)})
    button.textContent = "Send"
    button.removeAttribute("disabled")
    if (resp.status !== 200) {
        output.value = await resp.text()
        return
    }
    const link = form.querySelector("a#work")
    link.href = `/${bytesToHex(workhash)}`
    link.textContent = bytesToHex(workhash)
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

function concat(buffer1, buffer2) {
    const result = new Uint8Array(buffer1.length + buffer2.length)
    result.set(buffer1)
    result.set(buffer2, buffer1.length)
    return result
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
