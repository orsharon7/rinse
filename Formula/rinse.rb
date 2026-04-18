class Rinse < Formula
  desc "CLI automation tool for GitHub Copilot PR review workflows"
  homepage "https://github.com/orsharon7/rinse"
  # Update url + sha256 when a new release tag is cut that includes this formula.
  # Run: curl -sL <url> | shasum -a 256
  url "https://github.com/orsharon7/rinse/archive/refs/tags/v1.0.0.tar.gz"
  sha256 "0019dfc4b32d63c1392aa264aed2253c1e0c2fb09216f8e2cc269bbfb8bb49b5"
  license "BUSL-1.1"

  depends_on "go" => :build

  def install
    # Build the rinse binary from the repo root with version injection
    system "go", "build",
           "-ldflags", "-X main.version=#{version}",
           "-o", bin/"rinse",
           "."

    # Install pr-review helper scripts into libexec
    libexec.install Dir["scripts/*.sh"]
    chmod 0755, Dir["#{libexec}/*.sh"]

    # Create a pr-review wrapper that sets PR_REVIEW_SCRIPT_DIR automatically
    (bin/"pr-review").write <<~EOS
      #!/usr/bin/env bash
      export PR_REVIEW_SCRIPT_DIR="#{libexec}"
      exec "#{bin}/rinse" "$@"
    EOS
    chmod 0755, bin/"pr-review"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/rinse --version 2>&1")
    assert_predicate bin/"pr-review", :executable?
    assert_predicate libexec/"pr-review-launch.sh", :executable?
  end
end
